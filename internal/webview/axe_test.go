package webview

import (
	"testing"
	"time"
)

func TestReperesDuTempsRondsEtDates(t *testing.T) {
	bru, err := time.LoadLocation("Europe/Brussels")
	if err != nil {
		t.Skip("fuseau indisponible")
	}
	defer func(l *time.Location) { time.Local = l }(time.Local)
	time.Local = bru

	end := time.Date(2026, 11, 8, 21, 27, 0, 0, bru)
	cas := []struct {
		span time.Duration
		want []string
	}{
		{24 * time.Hour, []string{"00:00", "06:00", "12:00", "18:00"}},
		{6 * time.Hour, []string{"16:00", "18:00", "20:00"}},
		{7 * 24 * time.Hour, []string{"03/11", "05/11", "07/11"}},
	}
	for _, c := range cas {
		t0, t1 := end.Add(-c.span).Unix(), end.Unix()
		var got []string
		for _, ts := range axisTicks(t0, t1) {
			if ts < t0 || ts > t1 {
				t.Errorf("%s : repère hors de la période", c.span)
			}
			got = append(got, axisLabel(time.Unix(ts, 0), end, c.span))
		}
		if len(got) < 2 || len(got) > 4 {
			t.Errorf("%s : %d repères %v, attendu 2 à 4", c.span, len(got), got)
			continue
		}
		if c.want != nil && (len(got) != len(c.want) || got[0] != c.want[0] || got[len(got)-1] != c.want[len(c.want)-1]) {
			t.Errorf("%s : %v, attendu %v", c.span, got, c.want)
		}
	}
	// Une nuit : le jour precedent est nomme.
	nuit := time.Date(2026, 11, 8, 2, 30, 0, 0, bru)
	var got []string
	for _, ts := range axisTicks(nuit.Add(-6*time.Hour).Unix(), nuit.Unix()) {
		got = append(got, axisLabel(time.Unix(ts, 0), nuit, 6*time.Hour))
	}
	if len(got) == 0 || got[0] != "hier 22:00" || got[len(got)-1] != "02:00" {
		t.Errorf("nuit : %v", got)
	}
	// Une periode tres courte garde le debut et la fin.
	if ticks := axisTicks(end.Add(-20*time.Minute).Unix(), end.Unix()); len(ticks) != 2 {
		t.Errorf("20 min : %d repères", len(ticks))
	}
}
