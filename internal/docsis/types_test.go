package docsis

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fixture est un releve reel enregistre, servant de reference au pilote.
type fixture struct {
	ID          string      `json:"id"`
	Description string      `json:"description"`
	Raw         RawSnapshot `json:"raw"`
	PreviousRaw RawSnapshot `json:"previous_raw"`
}

func loadFixtures(t *testing.T) []fixture {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", "testdata", "*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("aucun cas trouve: %v", err)
	}
	var out []fixture
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		var f fixture
		if err := json.Unmarshal(data, &f); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		out = append(out, f)
	}
	return out
}

func TestFixturesDeserialisentSansPerte(t *testing.T) {
	for _, f := range loadFixtures(t) {
		if f.ID == "" {
			t.Errorf("cas sans identifiant")
		}
		snap := f.Raw.Normalize(time.Now())
		if len(snap.Downstream) != len(f.Raw.Downstream) {
			t.Errorf("%s: %d canaux descendants normalises pour %d bruts",
				f.ID, len(snap.Downstream), len(f.Raw.Downstream))
		}
		if len(snap.Upstream) != len(f.Raw.Upstream) {
			t.Errorf("%s: %d canaux montants normalises pour %d bruts",
				f.ID, len(snap.Upstream), len(f.Raw.Upstream))
		}
	}
}

func TestMSENegativeDevientSNRPositif(t *testing.T) {
	raw := RawSnapshot{Downstream: []RawDownstream{{ChannelID: 1, MSE: "-39.0"}}}
	got := raw.Normalize(time.Now()).Downstream[0].SNRDB
	if got == nil || *got != 39.0 {
		t.Fatalf("SNR attendu 39.0, obtenu %v", got)
	}
}

func TestValeurAbsenteResteNilEtPasZero(t *testing.T) {
	raw := RawSnapshot{Downstream: []RawDownstream{{ChannelID: 1, PowerLevel: "", MSE: "n/a"}}}
	ch := raw.Normalize(time.Now()).Downstream[0]
	if ch.PowerDBmV != nil {
		t.Errorf("puissance vide devrait rester nil, obtenu %v", *ch.PowerDBmV)
	}
	if ch.SNRDB != nil {
		t.Errorf("MSE illisible devrait rester nil, obtenu %v", *ch.SNRDB)
	}
}

func TestParseNumberAccepteLesUnitesEtLaVirgule(t *testing.T) {
	cases := map[string]*float64{
		"650 MHz": ptr(650), "43,5": ptr(43.5), "-39.0": ptr(-39),
		"+1.0": ptr(1), "1.0 dBmV": ptr(1), "": nil, "n/a": nil, "--": nil,
	}
	for input, want := range cases {
		got := ParseNumber(input)
		switch {
		case want == nil && got != nil:
			t.Errorf("%q: attendu nil, obtenu %v", input, *got)
		case want != nil && got == nil:
			t.Errorf("%q: attendu %v, obtenu nil", input, *want)
		case want != nil && *want != *got:
			t.Errorf("%q: attendu %v, obtenu %v", input, *want, *got)
		}
	}
}

func ptr(v float64) *float64 { return &v }

func TestChannelIDEnChaineEstAccepte(t *testing.T) {
	var raw RawSnapshot
	blob := `{"downstream":[{"channelID":"1","powerLevel":"not-a-number","mse":"bad"}],
	          "upstream":[{"channelID":2,"powerLevel":"bad"}]}`
	if err := json.Unmarshal([]byte(blob), &raw); err != nil {
		t.Fatalf("decodage refuse: %v", err)
	}
	snap := raw.Normalize(time.Now())
	if snap.Downstream[0].ChannelID != 1 || snap.Upstream[0].ChannelID != 2 {
		t.Fatalf("identifiants mal lus: %d et %d",
			snap.Downstream[0].ChannelID, snap.Upstream[0].ChannelID)
	}
	if snap.Downstream[0].PowerDBmV != nil || snap.Downstream[0].SNRDB != nil {
		t.Error("valeurs illisibles: attendu nil, pas zero")
	}
}
