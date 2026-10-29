package compensation

import (
	"testing"
	"time"
)

func TestBareme(t *testing.T) {
	h := time.Hour
	cas := []struct {
		d      time.Duration
		fee    float64
		amount float64
		floor  bool
	}{
		{7*h + 59*time.Minute, 0, 0, false}, // sous le seuil
		{8 * h, 0, 1, false},
		{23 * h, 0, 1, false},
		{24 * h, 0, 2, false},
		{47 * h, 0, 2, false},
		{48 * h, 0, 2.5, false},
		{72 * h, 0, 3, false},
		// 60 euros par mois : 2 euros par jour entame l'emportent sur le bareme.
		{10 * h, 60, 2, true},
		{30 * h, 60, 4, true},
		// 15 euros par mois : 0,50 euro par jour, le bareme l'emporte.
		{30 * h, 15, 2, false},
	}
	for _, c := range cas {
		r := Compute(c.d, c.fee)
		if r.Amount != c.amount || r.FloorApplied != c.floor || r.Eligible != (c.d >= Threshold) {
			t.Errorf("%v, %v €/mois : %+v, attendu %v € (plancher %v)", c.d, c.fee, r, c.amount, c.floor)
		}
	}
}
