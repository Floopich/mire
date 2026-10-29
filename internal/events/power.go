package events

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/store"
)

// KindPower est un saut de la puissance moyenne entre deux releves.
const KindPower = "puissance"

// Ecarts de puissance moyenne qui font un evenement. La voie montante, que
// le modem ajuste en permanence, demande un ecart plus grand.
const (
	powerShiftDown = 2.0
	powerShiftUp   = 3.0
)

// powerChanges compare la puissance moyenne des canaux presents dans les deux
// releves : un canal perdu ou retrouve ne doit pas passer pour un saut.
func powerChanges(prev, cur docsis.Snapshot) []store.Event {
	var out []store.Event

	before := map[int]*float64{}
	for _, c := range prev.Downstream {
		before[c.ChannelID] = c.PowerDBmV
	}
	var a, b []float64
	for _, c := range cur.Downstream {
		if p := before[c.ChannelID]; p != nil && c.PowerDBmV != nil {
			a, b = append(a, *p), append(b, *c.PowerDBmV)
		}
	}
	if e, ok := powerEvent(directionDown, "descendante", a, b, powerShiftDown); ok {
		out = append(out, e)
	}

	before = map[int]*float64{}
	for _, c := range prev.Upstream {
		before[c.ChannelID] = c.PowerDBmV
	}
	a, b = nil, nil
	for _, c := range cur.Upstream {
		if p := before[c.ChannelID]; p != nil && c.PowerDBmV != nil {
			a, b = append(a, *p), append(b, *c.PowerDBmV)
		}
	}
	if e, ok := powerEvent(directionUp, "montante", a, b, powerShiftUp); ok {
		out = append(out, e)
	}
	return out
}

func powerEvent(dir, label string, before, after []float64, limit float64) (store.Event, bool) {
	if len(before) == 0 {
		return store.Event{}, false
	}
	was, now := mean(before), mean(after)
	delta := now - was
	if math.Abs(delta) < limit {
		return store.Event{}, false
	}
	return store.Event{Kind: KindPower, Level: int(analyze.Warning), Direction: dir,
		Message: fmt.Sprintf("Saut de puissance %s : %s → %s dBmV (%s dB).",
			label, decimal(was), decimal(now), signed(delta))}, true
}

func mean(v []float64) float64 {
	var s float64
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

// decimal rend un nombre a une decimale, a la francaise : « −1,5 ».
func decimal(v float64) string {
	s := strconv.FormatFloat(math.Round(v*10)/10, 'f', 1, 64)
	s = strings.Replace(s, ".", ",", 1)
	return strings.Replace(s, "-", "−", 1)
}

// signed ajoute le signe plus a un ecart positif.
func signed(v float64) string {
	if v > 0 {
		return "+" + decimal(v)
	}
	return decimal(v)
}
