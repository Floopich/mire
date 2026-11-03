package snmp

import (
	"bytes"
	"context"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
	"time"
)

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// La requete de reference : GET de sysDescr.0, communaute public, telle que
// la montrent les captures de Wireshark (identifiant 1).
func TestEncodeGet(t *testing.T) {
	got := Encode("public", pduGet, 1, 0, 0, []OID{MustOID("1.3.6.1.2.1.1.1.0")})
	want := unhex(t, "30 26 02 01 01 04 06 70 75 62 6c 69 63 a0 19 02 01 01 02 01 00 02 01 00"+
		"30 0e 30 0c 06 08 2b 06 01 02 01 01 01 00 05 00")
	if !bytes.Equal(got, want) {
		t.Fatalf("\n%x\n%x", got, want)
	}
}

func TestEntiersEtOID(t *testing.T) {
	for _, v := range []int64{0, 1, 127, 128, 255, 256, -1, -128, -129, 2147483647, -2147483648} {
		raw := encodeInt(v)
		content, rest, err := element(raw, tagInteger)
		if err != nil || len(rest) != 0 || decodeInt(content) != v {
			t.Errorf("%d : %x", v, raw)
		}
	}
	// 4491 (CableLabs) s'ecrit sur deux octets : a3 0b.
	o := MustOID("1.3.6.1.4.1.4491.2.1.20.1.2.1.1.3")
	enc := encodeOID(o)
	if !bytes.Contains(enc, []byte{0xa3, 0x0b}) {
		t.Errorf("%x", enc)
	}
	back, err := decodeOID(enc)
	if err != nil || back.String() != o.String() {
		t.Errorf("%v %v", back, err)
	}
}

func TestDecodeReponse(t *testing.T) {
	// GetResponse : sysDescr.0 = "modem!", identifiant 1.
	b := unhex(t, "30 2c 02 01 01 04 06 70 75 62 6c 69 63 a2 1f 02 01 01 02 01 00 02 01 00"+
		"30 14 30 12 06 08 2b 06 01 02 01 01 01 00 04 06 6d 6f 64 65 6d 21")
	id, vbs, err := Decode(b)
	if err != nil || id != 1 || len(vbs) != 1 || vbs[0].OID.String() != "1.3.6.1.2.1.1.1.0" || string(vbs[0].Value.Bytes) != "modem!" {
		t.Fatalf("%d %+v %v", id, vbs, err)
	}
	// Erreur de l'agent : signalee, sans valeurs.
	b[len(b)-26] = 0x05 // error-status = genErr
	if _, _, err := Decode(b); err == nil || !strings.Contains(err.Error(), "erreur 5") {
		t.Errorf("erreur de l'agent : %v", err)
	}
	if _, _, err := Decode(b[:10]); err == nil {
		t.Error("message tronqué accepté")
	}
}

func TestParcoursDUneBranche(t *testing.T) {
	objects := map[string]Value{"1.3.6.1.2.1.1.1.0": {Kind: Octets, Bytes: []byte("Modem")}}
	for i := 1; i <= 45; i++ {
		objects["1.3.6.1.2.1.10.127.1.1.1.1.6."+strconv.Itoa(i)] = Value{Kind: Integer, Int: int64(-10 + i)}
	}
	objects["1.3.6.1.2.1.10.127.1.1.4.1.4.3"] = Value{Kind: Unsigned, Uint: 4294967295}
	objects["1.3.6.1.2.1.10.127.1.1.4.1.9.3"] = Value{Kind: Unsigned, Uint: 1 << 40}
	a, err := NewAgent("public", objects)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	c := &Client{Addr: a.Addr(), Community: "public", Timeout: time.Second, MaxRepetitions: 10}
	ctx := context.Background()

	vbs, err := c.Walk(ctx, MustOID("1.3.6.1.2.1.10.127.1.1.1.1.6"))
	if err != nil || len(vbs) != 45 {
		t.Fatalf("%d lignes, %v", len(vbs), err)
	}
	if vbs[0].Value.Int != -9 || vbs[44].OID.String() != "1.3.6.1.2.1.10.127.1.1.1.1.6.45" {
		t.Errorf("%+v %+v", vbs[0], vbs[44])
	}
	got, err := c.Get(ctx, MustOID("1.3.6.1.2.1.1.1.0"), MustOID("1.3.6.1.2.1.10.127.1.1.4.1.4.3"),
		MustOID("1.3.6.1.2.1.10.127.1.1.4.1.9.3"), MustOID("1.3.6.1.2.1.1.5.0"))
	if err != nil || string(got[0].Value.Bytes) != "Modem" || got[1].Value.Uint != 4294967295 ||
		got[2].Value.Uint != 1<<40 || got[3].Value.Kind != NoSuch {
		t.Fatalf("%+v %v", got, err)
	}
	// Communaute refusee : l'agent se tait.
	bad := &Client{Addr: c.Addr, Community: "privee", Timeout: 200 * time.Millisecond}
	if _, err := bad.Get(ctx, MustOID("1.3.6.1.2.1.1.1.0")); err != ErrTimeout {
		t.Errorf("communauté refusée : %v", err)
	}
}
