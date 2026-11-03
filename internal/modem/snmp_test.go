package modem

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/snmp"
)

// modemSNMP : deux canaux descendants (ifIndex 3 et 4), un montant (80),
// comme un modem DOCSIS 3.0 qui publie DOCS-IF-MIB et DOCS-IF3-MIB.
func modemSNMP(t *testing.T, community string, withTx3 bool) *snmp.Agent {
	t.Helper()
	i := func(n int64) snmp.Value { return snmp.Value{Kind: snmp.Integer, Int: n} }
	u := func(n uint64) snmp.Value { return snmp.Value{Kind: snmp.Unsigned, Uint: n} }
	o := map[string]snmp.Value{
		"1.3.6.1.2.1.1.1.0":          {Kind: snmp.Octets, Bytes: []byte("Cable Modem <<HW_REV: 1.0>>")},
		"1.3.6.1.2.1.10.127.1.1.5.0": i(4),
		// Voie descendante : identifiant, frequence (Hz), modulation, puissance (1/10 dBmV).
		"1.3.6.1.2.1.10.127.1.1.1.1.1.3": i(5), "1.3.6.1.2.1.10.127.1.1.1.1.1.4": i(2),
		"1.3.6.1.2.1.10.127.1.1.1.1.2.3": i(338000000), "1.3.6.1.2.1.10.127.1.1.1.1.2.4": i(330000000),
		"1.3.6.1.2.1.10.127.1.1.1.1.4.3": i(4), "1.3.6.1.2.1.10.127.1.1.1.1.4.4": i(3),
		"1.3.6.1.2.1.10.127.1.1.1.1.6.3": i(-23), "1.3.6.1.2.1.10.127.1.1.1.1.6.4": i(41),
		// Qualite : corrigees, non corrigees (32 bits), SNR (1/10 dB) ; 64 bits pour le canal 3.
		"1.3.6.1.2.1.10.127.1.1.4.1.3.3": u(10), "1.3.6.1.2.1.10.127.1.1.4.1.3.4": u(20),
		"1.3.6.1.2.1.10.127.1.1.4.1.4.3": u(1), "1.3.6.1.2.1.10.127.1.1.4.1.4.4": u(2),
		"1.3.6.1.2.1.10.127.1.1.4.1.5.3": i(385), "1.3.6.1.2.1.10.127.1.1.4.1.5.4": i(356),
		"1.3.6.1.2.1.10.127.1.1.4.1.9.3": u(5000000000),
		// Voie montante.
		"1.3.6.1.2.1.10.127.1.1.2.1.1.80": i(1), "1.3.6.1.2.1.10.127.1.1.2.1.2.80": i(36200000),
		"1.3.6.1.2.1.10.127.1.2.2.1.3.2": i(455),
	}
	if withTx3 {
		o["1.3.6.1.4.1.4491.2.1.20.1.2.1.1.80"] = i(472)
	}
	a, err := snmp.NewAgent(community, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestPiloteSNMP(t *testing.T) {
	a := modemSNMP(t, "public", true)
	d, err := NewSNMP(Config{URL: "http://" + a.Addr(), User: "public"})
	if err != nil {
		t.Fatal(err)
	}
	// L'adresse du modem garde son port : celui de l'agent de test.
	d.client.Addr = a.Addr()
	raw, err := d.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if raw.DOCSIS != "3.0" || len(raw.Downstream) != 2 || len(raw.Upstream) != 1 {
		t.Fatalf("%+v", raw)
	}
	snap := raw.Normalize(time.Now())
	ds2, ds5 := snap.Downstream[0], snap.Downstream[1]
	if ds2.ChannelID != 2 || ds2.Modulation != "64QAM" || *ds2.FrequencyMHz != 330 || *ds2.PowerDBmV != 4.1 || *ds2.SNRDB != 35.6 ||
		ds2.CorrErrors != 20 || ds2.NonCorrErrors != 2 {
		t.Errorf("canal 2 : %+v", ds2)
	}
	// Le compteur 64 bits l'emporte sur celui de 32 bits.
	if ds5.ChannelID != 5 || ds5.Modulation != "256QAM" || *ds5.PowerDBmV != -2.3 || *ds5.SNRDB != 38.5 || ds5.NonCorrErrors != 5000000000 {
		t.Errorf("canal 5 : %+v", ds5)
	}
	if us := snap.Upstream[0]; us.ChannelID != 1 || *us.FrequencyMHz != 36.2 || *us.PowerDBmV != 47.2 {
		t.Errorf("montant : %+v", us)
	}
}

func TestPiloteSNMPAncienneTable(t *testing.T) {
	// Sans DOCS-IF3-MIB, la puissance d'emission unique de DOCS-IF-MIB vaut
	// pour le seul canal montant.
	a := modemSNMP(t, "public", false)
	d, _ := NewSNMP(Config{URL: "http://192.168.100.1"})
	d.client.Addr = a.Addr()
	raw, err := d.Snapshot(context.Background())
	if err != nil || raw.Upstream[0].PowerLevel != "45.5" {
		t.Fatalf("%+v %v", raw.Upstream, err)
	}
}

func TestPiloteSNMPCommunauteRefusee(t *testing.T) {
	a := modemSNMP(t, "secret", true)
	d, _ := NewSNMP(Config{URL: "http://192.168.100.1", User: "public"})
	d.client.Addr, d.client.Timeout, d.client.Retries = a.Addr(), 200*time.Millisecond, 0
	if _, err := d.Snapshot(context.Background()); err == nil || !strings.Contains(err.Error(), "ne répond pas en SNMP") {
		t.Errorf("%v", err)
	}
	// Le mot de passe, s'il est renseigne, sert de communaute.
	d, _ = NewSNMP(Config{URL: "http://192.168.100.1", User: "public", Password: "secret"})
	d.client.Addr = a.Addr()
	if _, err := d.Snapshot(context.Background()); err != nil {
		t.Errorf("communauté du mot de passe : %v", err)
	}
}

func TestAdresseSNMP(t *testing.T) {
	for in, want := range map[string]string{"http://192.168.100.1": "192.168.100.1", "192.168.0.1": "192.168.0.1",
		"https://modem.lan:8443/": "modem.lan"} {
		if got, err := snmpHost(in); err != nil || got != want {
			t.Errorf("%s : %s %v", in, got, err)
		}
	}
	if _, err := snmpHost(""); err == nil {
		t.Error("adresse vide acceptée")
	}
}
