package events

import (
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/docsis"
)

// puissances fabrique un releve aux puissances donnees, canal par canal.
func puissances(at time.Time, down map[int]float64, up map[int]float64) docsis.Snapshot {
	s := docsis.Snapshot{Timestamp: at}
	for id, p := range down {
		s.Downstream = append(s.Downstream, docsis.Downstream{ChannelID: id, Modulation: "256QAM", PowerDBmV: &p})
	}
	for id, p := range up {
		s.Upstream = append(s.Upstream, docsis.Upstream{ChannelID: id, Modulation: "64QAM", PowerDBmV: &p})
	}
	return s
}

func TestSautDePuissance(t *testing.T) {
	prev := puissances(t0, map[int]float64{1: 3, 2: 4, 3: 5}, map[int]float64{1: 45, 2: 46})

	cas := []struct {
		nom        string
		down, up   map[int]float64
		attendu    string
		directions string
	}{
		{"variation ordinaire", map[int]float64{1: 3.5, 2: 4.8, 3: 5.4}, map[int]float64{1: 46, 2: 47}, "", ""},
		{"saut descendant", map[int]float64{1: 0.5, 2: 1.6, 3: 2.4}, map[int]float64{1: 45, 2: 46}, KindPower, "descendant"},
		{"reglage montant normal", map[int]float64{1: 3, 2: 4, 3: 5}, map[int]float64{1: 47.5, 2: 48.5}, "", ""},
		{"saut montant", map[int]float64{1: 3, 2: 4, 3: 5}, map[int]float64{1: 49, 2: 49.5}, KindPower, "montant"},
		// Le canal 3 disparait et le 9 arrive avec une puissance tres
		// differente : la moyenne ne porte que sur les canaux communs.
		{"canaux changes", map[int]float64{1: 3, 2: 4, 9: -6}, map[int]float64{1: 45, 2: 46}, "", ""},
	}
	for _, c := range cas {
		cur := puissances(t0.Add(10*time.Minute), c.down, c.up)
		evs := powerChanges(prev, cur)
		if kinds(evs) != c.attendu {
			t.Errorf("%s : %q, attendu %q", c.nom, kinds(evs), c.attendu)
			continue
		}
		if c.attendu != "" && evs[0].Direction != c.directions {
			t.Errorf("%s : direction %q", c.nom, evs[0].Direction)
		}
	}

	evs := powerChanges(prev, puissances(t0, map[int]float64{1: 0.5, 2: 1.6, 3: 2.4}, nil))
	if len(evs) != 1 || !strings.Contains(evs[0].Message, "4,0 → 1,5 dBmV (−2,5 dB)") {
		t.Errorf("message : %v", evs)
	}
}

func TestUnePuissanceAbsenteNeFaitPasDeSaut(t *testing.T) {
	prev := puissances(t0, map[int]float64{1: 3, 2: 4}, nil)
	cur := docsis.Snapshot{Timestamp: t0.Add(10 * time.Minute), Downstream: []docsis.Downstream{{ChannelID: 1}, {ChannelID: 2}}}
	if evs := powerChanges(prev, cur); len(evs) != 0 {
		t.Errorf("mesure absente prise pour un saut : %v", evs)
	}
}
