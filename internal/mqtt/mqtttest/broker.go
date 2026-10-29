// Package mqtttest fournit un serveur MQTT 3.1.1 minimal pour les tests : il
// accepte les connexions, retient les publications, repond aux pings et
// transmet aux abonnes ce que le test publie.
package mqtttest

import (
	"bufio"
	"io"
	"net"
	"sync"
	"testing"

	"github.com/floopich/mire/internal/mqtt"
)

// Connect decrit une demande de connexion recue.
type Connect struct {
	ClientID, User, Password string
	KeepAlive                int
	Will                     *mqtt.Message
}

// Broker est un serveur de test.
type Broker struct {
	// Refuse, s'il est non nul, est le code de refus renvoye a la connexion.
	Refuse byte

	ln       net.Listener
	mu       sync.Mutex
	connects []Connect
	messages []mqtt.Message
	pings    int
	closes   int
	subs     map[net.Conn][]string
}

// Start demarre un serveur sur une adresse locale ; il s'arrete avec le test.
func Start(t *testing.T) *Broker {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b := &Broker{ln: ln, subs: map[net.Conn][]string{}}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go b.serve(conn)
		}
	}()
	return b
}

// URL est l'adresse du serveur, en mqtt://.
func (b *Broker) URL() string { return "mqtt://" + b.ln.Addr().String() }

// Connects renvoie les connexions recues.
func (b *Broker) Connects() []Connect {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Connect(nil), b.connects...)
}

// Messages renvoie les publications recues, dans l'ordre.
func (b *Broker) Messages() []mqtt.Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]mqtt.Message(nil), b.messages...)
}

// Retained renvoie le dernier message retenu de chaque sujet.
func (b *Broker) Retained() map[string]string {
	out := map[string]string{}
	for _, m := range b.Messages() {
		if m.Retain {
			out[m.Topic] = string(m.Payload)
		}
	}
	return out
}

// Pings et Closes comptent les pings et les deconnexions propres.
func (b *Broker) Pings() int  { b.mu.Lock(); defer b.mu.Unlock(); return b.pings }
func (b *Broker) Closes() int { b.mu.Lock(); defer b.mu.Unlock(); return b.closes }

// Subscriptions renvoie les sujets suivis par les connexions ouvertes.
func (b *Broker) Subscriptions() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for _, ts := range b.subs {
		out = append(out, ts...)
	}
	return out
}

// Send publie un message vers les connexions abonnees a son sujet, comme le
// ferait un autre client, Home Assistant par exemple.
func (b *Broker) Send(m mqtt.Message) {
	var header byte = 0x30
	if m.Retain {
		header |= 0x01
	}
	body := append([]byte{byte(len(m.Topic) >> 8), byte(len(m.Topic))}, m.Topic...)
	body = append(body, m.Payload...)
	pkt := append([]byte{header, byte(len(body))}, body...) // messages courts
	b.mu.Lock()
	defer b.mu.Unlock()
	for conn, ts := range b.subs {
		for _, t := range ts {
			if t == m.Topic {
				_, _ = conn.Write(pkt)
			}
		}
	}
}

func (b *Broker) serve(conn net.Conn) {
	defer func() {
		b.mu.Lock()
		delete(b.subs, conn)
		b.mu.Unlock()
		_ = conn.Close()
	}()
	r := bufio.NewReader(conn)
	for {
		header, body, err := readPacket(r)
		if err != nil {
			return
		}
		switch header & 0xF0 {
		case 0x10:
			c := parseConnect(body)
			b.mu.Lock()
			b.connects = append(b.connects, c)
			b.mu.Unlock()
			if _, err := conn.Write([]byte{0x20, 2, 0, b.Refuse}); err != nil || b.Refuse != 0 {
				return
			}
		case 0x30:
			topic, rest := str(body)
			b.mu.Lock()
			b.messages = append(b.messages, mqtt.Message{Topic: topic, Payload: rest, Retain: header&0x01 != 0})
			b.mu.Unlock()
		case 0x80:
			if len(body) < 2 {
				return
			}
			topic, _ := str(body[2:])
			b.mu.Lock()
			b.subs[conn] = append(b.subs[conn], topic)
			_, err := conn.Write([]byte{0x90, 3, body[0], body[1], 0})
			b.mu.Unlock()
			if err != nil {
				return
			}
		case 0xC0:
			b.mu.Lock()
			b.pings++
			b.mu.Unlock()
			if _, err := conn.Write([]byte{0xD0, 0}); err != nil {
				return
			}
		case 0xE0:
			b.mu.Lock()
			b.closes++
			b.mu.Unlock()
			return
		}
	}
}

func readPacket(r *bufio.Reader) (byte, []byte, error) {
	header, err := r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	n, mult := 0, 1
	for {
		c, err := r.ReadByte()
		if err != nil {
			return 0, nil, err
		}
		n += int(c&0x7F) * mult
		if c&0x80 == 0 {
			break
		}
		mult *= 128
	}
	body := make([]byte, n)
	_, err = io.ReadFull(r, body)
	return header, body, err
}

func str(b []byte) (string, []byte) {
	if len(b) < 2 {
		return "", nil
	}
	n := int(b[0])<<8 | int(b[1])
	if len(b) < 2+n {
		return "", nil
	}
	return string(b[2 : 2+n]), b[2+n:]
}

func parseConnect(body []byte) Connect {
	_, rest := str(body) // « MQTT »
	if len(rest) < 4 {
		return Connect{}
	}
	flags := rest[1]
	c := Connect{KeepAlive: int(rest[2])<<8 | int(rest[3])}
	rest = rest[4:]
	c.ClientID, rest = str(rest)
	if flags&0x04 != 0 {
		var topic, payload string
		topic, rest = str(rest)
		payload, rest = str(rest)
		c.Will = &mqtt.Message{Topic: topic, Payload: []byte(payload), Retain: flags&0x20 != 0}
	}
	if flags&0x80 != 0 {
		c.User, rest = str(rest)
	}
	if flags&0x40 != 0 {
		c.Password, _ = str(rest)
	}
	return c
}
