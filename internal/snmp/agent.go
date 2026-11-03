package snmp

import (
	"net"
	"sort"
)

// Agent est un agent SNMPv2c minimal, en lecture seule, pour les tests : il
// repond aux GET et aux GETBULK sur un jeu d'objets, et se tait devant une
// communaute inconnue, comme un vrai agent.
type Agent struct {
	conn      net.PacketConn
	community string
	objects   map[string]Value
	order     []OID
}

// NewAgent ecoute sur une adresse locale libre.
func NewAgent(community string, objects map[string]Value) (*Agent, error) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	a := &Agent{conn: conn, community: community, objects: objects}
	for k := range objects {
		a.order = append(a.order, MustOID(k))
	}
	sort.Slice(a.order, func(i, j int) bool { return compare(a.order[i], a.order[j]) < 0 })
	go a.serve()
	return a, nil
}

// Addr est l'adresse de l'agent, hote:port.
func (a *Agent) Addr() string { return a.conn.LocalAddr().String() }

// Close arrete l'agent.
func (a *Agent) Close() error { return a.conn.Close() }

func encodeValue(v Value) []byte {
	switch v.Kind {
	case Integer:
		return encodeInt(v.Int)
	case Unsigned:
		var b []byte
		for n := v.Uint; n > 0; n >>= 8 {
			b = append([]byte{byte(n)}, b...)
		}
		if len(b) == 0 || b[0]&0x80 != 0 {
			b = append([]byte{0}, b...)
		}
		tag := byte(tagCounter32)
		if v.Uint > 0xffffffff {
			tag = tagCounter64
		}
		return tlv(tag, b)
	case Octets:
		return tlv(tagOctets, v.Bytes)
	case ObjectID:
		return tlv(tagOID, encodeOID(v.OID))
	}
	return []byte{tagNull, 0}
}

func (a *Agent) serve() {
	buf := make([]byte, 65535)
	for {
		n, from, err := a.conn.ReadFrom(buf)
		if err != nil {
			return
		}
		msg, _, err := element(buf[:n], tagSequence)
		if err != nil {
			continue
		}
		_, msg, _ = element(msg, tagInteger)
		comm, msg, err := element(msg, tagOctets)
		if err != nil || string(comm) != a.community || len(msg) == 0 {
			continue
		}
		tag := msg[0]
		pdu, _, _ := element(msg, tag)
		rawID, pdu, _ := element(pdu, tagInteger)
		_, pdu, _ = element(pdu, tagInteger)
		rawRep, pdu, _ := element(pdu, tagInteger)
		list, _, _ := element(pdu, tagSequence)
		var reqs []OID
		for len(list) > 0 {
			var vb []byte
			vb, list, _ = element(list, tagSequence)
			raw, _, _ := element(vb, tagOID)
			o, err := decodeOID(raw)
			if err != nil {
				break
			}
			reqs = append(reqs, o)
		}
		var out []byte
		add := func(o OID, val []byte) {
			out = append(out, tlv(tagSequence, append(tlv(tagOID, encodeOID(o)), val...))...)
		}
		for _, o := range reqs {
			if tag == pduGet {
				if v, ok := a.objects[o.String()]; ok {
					add(o, encodeValue(v))
				} else {
					add(o, []byte{tagNoSuchObj, 0})
				}
				continue
			}
			left := int(decodeInt(rawRep))
			for _, k := range a.order {
				if left == 0 {
					break
				}
				if compare(k, o) > 0 {
					add(k, encodeValue(a.objects[k.String()]))
					left--
				}
			}
			if left > 0 {
				add(o, []byte{tagEndOfMib, 0})
			}
		}
		body := append(append(tlv(tagInteger, rawID), encodeInt(0)...), encodeInt(0)...)
		body = append(body, tlv(tagSequence, out)...)
		resp := append(encodeInt(version2c), tlv(tagOctets, comm)...)
		resp = append(resp, tlv(pduResponse, body)...)
		_, _ = a.conn.WriteTo(tlv(tagSequence, resp), from)
	}
}
