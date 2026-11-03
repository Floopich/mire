// Package snmp est un client SNMPv2c minimal, en lecture seule : GET et
// parcours d'une branche par GETBULK. Il suffit aux tables DOCSIS des modems
// cable, sans dependance externe.
package snmp

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// Types BER et SNMP.
const (
	tagInteger   = 0x02
	tagOctets    = 0x04
	tagNull      = 0x05
	tagOID       = 0x06
	tagSequence  = 0x30
	tagIPAddress = 0x40
	tagCounter32 = 0x41
	tagGauge32   = 0x42
	tagTimeTicks = 0x43
	tagOpaque    = 0x44
	tagCounter64 = 0x46
	tagNoSuchObj = 0x80
	tagNoSuchIns = 0x81
	tagEndOfMib  = 0x82

	pduGet      = 0xa0
	pduResponse = 0xa2
	pduGetBulk  = 0xa5

	version2c = 1
)

// OID est un identifiant d'objet : 1.3.6.1.2.1.1.1.0.
type OID []uint32

// ParseOID lit une OID ecrite en points.
func ParseOID(s string) (OID, error) {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(s), "."), ".")
	out := make(OID, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("OID %q illisible", s)
		}
		out = append(out, uint32(n))
	}
	if len(out) < 2 {
		return nil, fmt.Errorf("OID %q trop courte", s)
	}
	return out, nil
}

// MustOID lit une OID ecrite dans le code.
func MustOID(s string) OID {
	o, err := ParseOID(s)
	if err != nil {
		panic(err)
	}
	return o
}

func (o OID) String() string {
	parts := make([]string, len(o))
	for i, n := range o {
		parts[i] = strconv.FormatUint(uint64(n), 10)
	}
	return strings.Join(parts, ".")
}

// HasPrefix dit si o est dans la branche p.
func (o OID) HasPrefix(p OID) bool {
	if len(o) < len(p) {
		return false
	}
	for i := range p {
		if o[i] != p[i] {
			return false
		}
	}
	return true
}

// Kind dit la nature d'une valeur.
type Kind int

const (
	Null Kind = iota
	Integer
	Unsigned // Counter32, Gauge32, TimeTicks, Counter64
	Octets
	ObjectID
	NoSuch   // noSuchObject, noSuchInstance
	EndOfMib // fin de la branche
)

// Value est une valeur lue.
type Value struct {
	Kind  Kind
	Int   int64
	Uint  uint64
	Bytes []byte
	OID   OID
}

// VarBind associe une OID a sa valeur.
type VarBind struct {
	OID   OID
	Value Value
}

// Client interroge un agent SNMPv2c.
type Client struct {
	// Addr est hote:port ; le port 161 par defaut.
	Addr      string
	Community string
	Timeout   time.Duration
	Retries   int
	// MaxRepetitions borne les lignes demandees par GETBULK.
	MaxRepetitions int
}

// ErrTimeout signale un agent muet : SNMP ferme, communaute refusee (un
// agent ne repond pas a une communaute inconnue) ou modem injoignable.
var ErrTimeout = errors.New("pas de réponse SNMP")

func (c *Client) addr() string {
	if _, _, err := net.SplitHostPort(c.Addr); err == nil {
		return c.Addr
	}
	return net.JoinHostPort(c.Addr, "161")
}

// Get lit des objets precis.
func (c *Client) Get(ctx context.Context, oids ...OID) ([]VarBind, error) {
	return c.exchange(ctx, pduGet, 0, 0, oids)
}

// Walk parcourt la branche root, dans l'ordre des OID.
func (c *Client) Walk(ctx context.Context, root OID) ([]VarBind, error) {
	rep := c.MaxRepetitions
	if rep <= 0 {
		rep = 20
	}
	var out []VarBind
	next := root
	for range 100000 {
		vbs, err := c.exchange(ctx, pduGetBulk, 0, rep, []OID{next})
		if err != nil {
			return out, err
		}
		if len(vbs) == 0 {
			return out, nil
		}
		for _, vb := range vbs {
			if vb.Value.Kind == EndOfMib || !vb.OID.HasPrefix(root) {
				return out, nil
			}
			if len(out) > 0 && compare(vb.OID, out[len(out)-1].OID) <= 0 {
				return out, errors.New("agent SNMP : OID non croissantes")
			}
			out = append(out, vb)
		}
		next = vbs[len(vbs)-1].OID
	}
	return out, errors.New("agent SNMP : branche sans fin")
}

func compare(a, b OID) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return len(a) - len(b)
}

func (c *Client) exchange(ctx context.Context, pdu byte, nonRep, maxRep int, oids []OID) ([]VarBind, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", c.addr())
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	buf := make([]byte, 65535)
	for attempt := 0; attempt <= max(c.Retries, 0); attempt++ {
		id := requestID()
		msg := Encode(c.Community, pdu, id, nonRep, maxRep, oids)
		deadline := time.Now().Add(timeout)
		if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
			deadline = dl
		}
		_ = conn.SetDeadline(deadline)
		if _, err := conn.Write(msg); err != nil {
			return nil, err
		}
		for {
			n, err := conn.Read(buf)
			if err != nil {
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					break // nouvelle tentative
				}
				return nil, err
			}
			rid, vbs, err := Decode(buf[:n])
			if err != nil {
				return nil, err
			}
			if rid != id {
				continue // reponse a une tentative precedente
			}
			return vbs, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, ErrTimeout
}

func requestID() int32 {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return int32(binary.BigEndian.Uint32(b[:]) & 0x7fffffff)
}

// Encode construit une requete. Elle est exportee pour les tests.
func Encode(community string, pdu byte, id int32, nonRep, maxRep int, oids []OID) []byte {
	var list []byte
	for _, o := range oids {
		list = append(list, tlv(tagSequence, append(tlv(tagOID, encodeOID(o)), tagNull, 0))...)
	}
	body := append(encodeInt(int64(id)), encodeInt(int64(nonRep))...)
	body = append(body, encodeInt(int64(maxRep))...)
	body = append(body, tlv(tagSequence, list)...)
	msg := append(encodeInt(version2c), tlv(tagOctets, []byte(community))...)
	msg = append(msg, tlv(pdu, body)...)
	return tlv(tagSequence, msg)
}

func tlv(tag byte, content []byte) []byte {
	out := []byte{tag}
	n := len(content)
	switch {
	case n < 0x80:
		out = append(out, byte(n))
	case n <= 0xff:
		out = append(out, 0x81, byte(n))
	default:
		out = append(out, 0x82, byte(n>>8), byte(n))
	}
	return append(out, content...)
}

func encodeInt(v int64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(v))
	// Retire les octets de tete superflus, en gardant le signe.
	for len(b) > 1 && ((b[0] == 0 && b[1]&0x80 == 0) || (b[0] == 0xff && b[1]&0x80 != 0)) {
		b = b[1:]
	}
	return tlv(tagInteger, b)
}

func encodeOID(o OID) []byte {
	out := []byte{byte(o[0]*40 + o[1])}
	for _, n := range o[2:] {
		var tmp []byte
		tmp = append(tmp, byte(n&0x7f))
		for n >>= 7; n > 0; n >>= 7 {
			tmp = append(tmp, byte(n&0x7f)|0x80)
		}
		for i := len(tmp) - 1; i >= 0; i-- {
			out = append(out, tmp[i])
		}
	}
	return out
}

// Decode lit une reponse : son identifiant et ses valeurs.
func Decode(b []byte) (int32, []VarBind, error) {
	msg, rest, err := element(b, tagSequence)
	if err != nil || len(rest) != 0 {
		return 0, nil, errors.New("réponse SNMP illisible")
	}
	if _, msg, err = element(msg, tagInteger); err != nil { // version
		return 0, nil, errors.New("réponse SNMP illisible")
	}
	if _, msg, err = element(msg, tagOctets); err != nil { // communaute
		return 0, nil, errors.New("réponse SNMP illisible")
	}
	pdu, _, err := element(msg, pduResponse)
	if err != nil {
		return 0, nil, errors.New("réponse SNMP inattendue")
	}
	var fields [3]int64
	for i := range fields {
		var raw []byte
		if raw, pdu, err = element(pdu, tagInteger); err != nil {
			return 0, nil, errors.New("réponse SNMP illisible")
		}
		fields[i] = decodeInt(raw)
	}
	if fields[1] != 0 {
		return int32(fields[0]), nil, fmt.Errorf("agent SNMP : erreur %d", fields[1])
	}
	list, _, err := element(pdu, tagSequence)
	if err != nil {
		return 0, nil, errors.New("réponse SNMP illisible")
	}
	var out []VarBind
	for len(list) > 0 {
		var vb []byte
		if vb, list, err = element(list, tagSequence); err != nil {
			return 0, nil, errors.New("réponse SNMP illisible")
		}
		rawOID, rest, err := element(vb, tagOID)
		if err != nil {
			return 0, nil, errors.New("réponse SNMP illisible")
		}
		oid, err := decodeOID(rawOID)
		if err != nil {
			return 0, nil, err
		}
		if len(rest) < 2 {
			return 0, nil, errors.New("réponse SNMP illisible")
		}
		tag := rest[0]
		content, _, err := element(rest, tag)
		if err != nil {
			return 0, nil, errors.New("réponse SNMP illisible")
		}
		var v Value
		switch tag {
		case tagInteger:
			v = Value{Kind: Integer, Int: decodeInt(content)}
		case tagCounter32, tagGauge32, tagTimeTicks, tagCounter64:
			v = Value{Kind: Unsigned, Uint: decodeUint(content)}
		case tagOctets, tagIPAddress, tagOpaque:
			v = Value{Kind: Octets, Bytes: append([]byte(nil), content...)}
		case tagOID:
			o, err := decodeOID(content)
			if err != nil {
				return 0, nil, err
			}
			v = Value{Kind: ObjectID, OID: o}
		case tagNoSuchObj, tagNoSuchIns:
			v = Value{Kind: NoSuch}
		case tagEndOfMib:
			v = Value{Kind: EndOfMib}
		default:
			v = Value{Kind: Null}
		}
		out = append(out, VarBind{OID: oid, Value: v})
	}
	return int32(fields[0]), out, nil
}

// element lit un element du type attendu ; il renvoie son contenu et la
// suite.
func element(b []byte, tag byte) (content, rest []byte, err error) {
	if len(b) < 2 || b[0] != tag {
		return nil, nil, errors.New("type inattendu")
	}
	n, head := int(b[1]), 2
	if n&0x80 != 0 {
		k := n & 0x7f
		if k == 0 || k > 3 || len(b) < 2+k {
			return nil, nil, errors.New("longueur illisible")
		}
		n = 0
		for _, c := range b[2 : 2+k] {
			n = n<<8 | int(c)
		}
		head = 2 + k
	}
	if len(b) < head+n {
		return nil, nil, errors.New("message tronqué")
	}
	return b[head : head+n], b[head+n:], nil
}

func decodeInt(b []byte) int64 {
	if len(b) == 0 {
		return 0
	}
	var v int64
	if b[0]&0x80 != 0 {
		v = -1
	}
	for _, c := range b {
		v = v<<8 | int64(c)
	}
	return v
}

func decodeUint(b []byte) uint64 {
	var v uint64
	for _, c := range b {
		v = v<<8 | uint64(c)
	}
	return v
}

func decodeOID(b []byte) (OID, error) {
	if len(b) == 0 {
		return nil, errors.New("OID vide")
	}
	out := OID{uint32(b[0]) / 40, uint32(b[0]) % 40}
	if b[0] >= 80 {
		out = OID{2, uint32(b[0]) - 80}
	}
	var n uint32
	for i, c := range b[1:] {
		n = n<<7 | uint32(c&0x7f)
		if c&0x80 == 0 {
			out = append(out, n)
			n = 0
		} else if i == len(b)-2 {
			return nil, errors.New("OID tronquée")
		}
	}
	return out, nil
}
