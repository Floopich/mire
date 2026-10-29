// Package mqtt parle a un serveur MQTT 3.1.1.
//
// Mire publie son etat et ecoute un seul sujet de commande : connexion,
// publication et abonnement en QoS 0, maintien de la connexion et
// deconnexion. Ces quelques paquets de la norme OASIS MQTT 3.1.1 tiennent
// ici sans dependance ; un message retenu est republie a chaque reconnexion,
// ce qui rend le QoS 1 inutile.
package mqtt

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"sync"
	"syscall"
	"time"
)

// Message est une publication.
type Message struct {
	Topic   string
	Payload []byte
	// Retain demande au serveur de garder le message pour les abonnes a venir ;
	// sur un message recu, il signale un message retenu, publie avant
	// l'abonnement.
	Retain bool
}

// Options regle la connexion.
type Options struct {
	// Server est l'adresse du serveur : mqtt://hote:1883 ou mqtts://hote:8883.
	Server   string
	User     string
	Password string
	ClientID string
	// Will est publie par le serveur si la connexion se perd sans adieu.
	Will *Message
	// KeepAlive est le delai maximal entre deux paquets ; 60 s par defaut.
	KeepAlive time.Duration
	// Timeout borne chaque echange ; 10 s par defaut.
	Timeout time.Duration
}

// ErrRefused signale un refus du serveur, distinct d'une panne reseau.
var ErrRefused = errors.New("connexion refusée par le serveur MQTT")

var refusals = map[byte]string{
	1: "version du protocole non prise en charge",
	2: "identifiant de client refusé",
	3: "service indisponible",
	4: "identifiant ou mot de passe incorrect",
	5: "accès non autorisé",
}

// Client est une connexion etablie. Une fois connecte, il lit en continu ce
// que le serveur envoie : reponses aux pings et messages des abonnements.
type Client struct {
	conn    net.Conn
	r       *bufio.Reader
	timeout time.Duration

	wmu    sync.Mutex // une ecriture a la fois
	nextID uint16
	pong   chan struct{}
	inbox  chan Message
	done   chan struct{}
	err    error // fixe avant la fermeture de done
}

// Dial ouvre la connexion et s'identifie.
func Dial(ctx context.Context, o Options) (*Client, error) {
	u, err := url.Parse(o.Server)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("adresse du serveur MQTT invalide : %q", o.Server)
	}
	if o.KeepAlive <= 0 {
		o.KeepAlive = time.Minute
	}
	if o.Timeout <= 0 {
		o.Timeout = 10 * time.Second
	}
	port := u.Port()
	dialer := &net.Dialer{Timeout: o.Timeout}
	var conn net.Conn
	switch u.Scheme {
	case "mqtt":
		if port == "" {
			port = "1883"
		}
		conn, err = dialer.DialContext(ctx, "tcp", net.JoinHostPort(u.Hostname(), port))
	case "mqtts":
		if port == "" {
			port = "8883"
		}
		td := &tls.Dialer{NetDialer: dialer, Config: &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12}}
		conn, err = td.DialContext(ctx, "tcp", net.JoinHostPort(u.Hostname(), port))
	default:
		return nil, fmt.Errorf("adresse du serveur MQTT : mqtt:// ou mqtts:// attendu, pas %q", u.Scheme)
	}
	if err != nil {
		return nil, fmt.Errorf("serveur MQTT injoignable : %s", reason(err))
	}
	c := &Client{conn: conn, r: bufio.NewReader(conn), timeout: o.Timeout,
		pong: make(chan struct{}, 1), inbox: make(chan Message, 8), done: make(chan struct{})}
	if err := c.connect(o); err != nil {
		_ = conn.Close()
		return nil, err
	}
	go c.listen()
	return c, nil
}

// listen lit les paquets du serveur jusqu'a la fin de la connexion.
func (c *Client) listen() {
	_ = c.conn.SetReadDeadline(time.Time{})
	for {
		header, body, err := c.next()
		if err != nil {
			c.err = err
			close(c.done)
			return
		}
		switch header & 0xF0 {
		case 0xD0:
			select {
			case c.pong <- struct{}{}:
			default:
			}
		case 0x30:
			m, id, ok := parsePublish(header, body)
			if !ok {
				continue
			}
			if id != nil {
				// QoS 1 : le serveur attend l'accuse de reception.
				_ = c.write(0x40, id)
			}
			select {
			case c.inbox <- m:
			default: // personne n'ecoute : le message est perdu, comme en QoS 0
			}
		}
	}
}

// parsePublish lit un message recu ; id porte l'identifiant a accuser en
// QoS 1, nil en QoS 0.
func parsePublish(header byte, body []byte) (m Message, id []byte, ok bool) {
	if len(body) < 2 {
		return m, nil, false
	}
	n := int(body[0])<<8 | int(body[1])
	if len(body) < 2+n {
		return m, nil, false
	}
	m.Topic, body = string(body[2:2+n]), body[2+n:]
	if qos := (header >> 1) & 0x03; qos > 0 {
		if len(body) < 2 {
			return m, nil, false
		}
		id, body = body[:2], body[2:]
		if qos == 2 {
			// Jamais demande : l'abonnement est en QoS 0.
			return m, nil, false
		}
	}
	m.Payload, m.Retain = body, header&0x01 != 0
	return m, id, true
}

// Messages rend les messages recus sur les sujets suivis.
func (c *Client) Messages() <-chan Message { return c.inbox }

// Done est ferme quand la connexion est perdue ; Err dit pourquoi.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err renvoie la cause de la fin de connexion, une fois Done ferme.
func (c *Client) Err() error {
	select {
	case <-c.done:
		return c.err
	default:
		return nil
	}
}

// Subscribe suit un sujet, en QoS 0.
func (c *Client) Subscribe(topic string) error {
	c.wmu.Lock()
	c.nextID++
	if c.nextID == 0 {
		c.nextID = 1
	}
	id := c.nextID
	c.wmu.Unlock()
	body := []byte{byte(id >> 8), byte(id)}
	body = appendString(body, topic)
	body = append(body, 0)
	return c.write(0x82, body)
}

func (c *Client) connect(o Options) error {
	var flags byte = 0x02 // session propre
	var payload []byte
	payload = appendString(payload, o.ClientID)
	if o.Will != nil {
		flags |= 0x04
		if o.Will.Retain {
			flags |= 0x20
		}
		payload = appendString(payload, o.Will.Topic)
		payload = appendBytes(payload, o.Will.Payload)
	}
	if o.User != "" {
		flags |= 0x80
		payload = appendString(payload, o.User)
		if o.Password != "" {
			flags |= 0x40
			payload = appendString(payload, o.Password)
		}
	}
	keep := int(o.KeepAlive / time.Second)
	if keep > 0xFFFF {
		keep = 0xFFFF
	}
	body := appendString(nil, "MQTT")
	body = append(body, 4, flags, byte(keep>>8), byte(keep))
	body = append(body, payload...)
	if err := c.write(0x10, body); err != nil {
		return err
	}
	kind, rest, err := c.read()
	if err != nil {
		return fmt.Errorf("serveur MQTT : pas de réponse à la connexion : %w", err)
	}
	if kind != 0x20 || len(rest) != 2 {
		return fmt.Errorf("serveur MQTT : réponse inattendue à la connexion (paquet %#x)", kind)
	}
	if code := rest[1]; code != 0 {
		reason, ok := refusals[code]
		if !ok {
			reason = fmt.Sprintf("code %d", code)
		}
		return fmt.Errorf("%w : %s", ErrRefused, reason)
	}
	return nil
}

// reason traduit une erreur reseau en une phrase, sans le detail technique.
func reason(err error) string {
	var dns *net.DNSError
	var cert *tls.CertificateVerificationError
	var opErr *net.OpError
	switch {
	case errors.As(err, &dns):
		return "nom de serveur inconnu"
	case errors.As(err, &cert):
		return "certificat TLS refusé"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return "pas de réponse"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connexion refusée, aucun serveur MQTT sur ce port"
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		return "machine inaccessible"
	case errors.As(err, &opErr) && opErr.Timeout():
		return "pas de réponse"
	default:
		return err.Error()
	}
}

// Publish envoie un message en QoS 0.
func (c *Client) Publish(m Message) error {
	var header byte = 0x30
	if m.Retain {
		header |= 0x01
	}
	body := appendString(nil, m.Topic)
	body = append(body, m.Payload...)
	return c.write(header, body)
}

// Ping verifie que le serveur repond encore.
func (c *Client) Ping() error {
	select {
	case <-c.pong: // reponse tardive a un ping precedent
	default:
	}
	if err := c.write(0xC0, nil); err != nil {
		return err
	}
	select {
	case <-c.pong:
		return nil
	case <-c.done:
		return fmt.Errorf("serveur MQTT muet : %w", c.err)
	case <-time.After(c.timeout):
		return errors.New("serveur MQTT muet : pas de réponse au ping")
	}
}

// Close se deconnecte proprement : le message de derniere volonte n'est pas
// publie.
func (c *Client) Close() error {
	werr := c.write(0xE0, nil)
	cerr := c.conn.Close()
	return errors.Join(werr, cerr)
}

func (c *Client) write(header byte, body []byte) error {
	if len(body) > 268_435_455 {
		return errors.New("message MQTT trop long")
	}
	pkt := append([]byte{header}, remaining(len(body))...)
	pkt = append(pkt, body...)
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(c.timeout))
	_, err := c.conn.Write(pkt)
	return err
}

// read lit la reponse a la connexion : son type (4 bits de poids fort) et
// son contenu.
func (c *Client) read() (byte, []byte, error) {
	_ = c.conn.SetReadDeadline(time.Now().Add(c.timeout))
	header, body, err := c.next()
	return header & 0xF0, body, err
}

// next lit un paquet entier : son octet d'en-tete et son contenu.
func (c *Client) next() (byte, []byte, error) {
	header, err := c.r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	n, mult := 0, 1
	for i := 0; ; i++ {
		b, err := c.r.ReadByte()
		if err != nil {
			return 0, nil, err
		}
		n += int(b&0x7F) * mult
		if b&0x80 == 0 {
			break
		}
		if i == 3 {
			return 0, nil, errors.New("longueur de paquet MQTT invalide")
		}
		mult *= 128
	}
	rest := make([]byte, n)
	if _, err := io.ReadFull(c.r, rest); err != nil {
		return 0, nil, err
	}
	return header, rest, nil
}

// remaining encode une longueur sur un a quatre octets (norme, 2.2.3).
func remaining(n int) []byte {
	var out []byte
	for {
		b := byte(n % 128)
		n /= 128
		if n > 0 {
			b |= 0x80
		}
		out = append(out, b)
		if n == 0 {
			return out
		}
	}
}

func appendString(b []byte, s string) []byte { return appendBytes(b, []byte(s)) }

func appendBytes(b, s []byte) []byte {
	return append(append(b, byte(len(s)>>8), byte(len(s))), s...)
}
