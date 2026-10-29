package webview

import (
	"context"
	"html/template"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/floopich/mire/internal/settings"
	"github.com/floopich/mire/internal/store"
)

// Le debit vient d'un Speedtest Tracker ou d'une passerelle UniFi : Mire
// n'en mesure pas. La page
// Internet et le rapport en donnent la mediane, moins sensible qu'une
// moyenne a une mesure ratee.

// speedView resume les mesures de debit d'une periode.
type speedView struct {
	// Shown dit si la section a lieu d'etre : la lecture est active, ou des
	// mesures sont en base.
	Shown bool
	// Source nomme d'ou viennent les mesures.
	Source string
	Count  int
	// LastEver est la derniere mesure connue, hors de la periode.
	LastEver time.Time
	Down, Up string
	DownLow  string
	Last     time.Time
	LastDown string
	LastUp   string
	Charts   []template.HTML
}

// mbit rend un debit : « 948 Mbit/s », au dixieme sous 100.
func mbit(v float64) string {
	if v < 100 {
		return dec(v) + nbsp + "Mbit/s"
	}
	return strconv.Itoa(int(math.Round(v))) + nbsp + "Mbit/s"
}

func median(values []float64) float64 {
	s := append([]float64(nil), values...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// speedStats renvoie les medianes descendante et montante et le plus bas
// debit descendant ; ok est faux sans mesure.
func speedStats(tests []store.SpeedTest) (down, up, low float64, ok bool) {
	if len(tests) == 0 {
		return 0, 0, 0, false
	}
	var d, u []float64
	low = math.Inf(1)
	for _, t := range tests {
		d, u = append(d, t.Download), append(u, t.Upload)
		low = math.Min(low, t.Download)
	}
	return median(d), median(u), low, true
}

// speedGap interrompt la courbe : au-dela, des mesures manquent.
const speedGap = 6 * time.Hour

func (s *Server) speedFor(ctx context.Context, from, to time.Time) (speedView, error) {
	var v speedView
	tests, err := store.SpeedTests(ctx, s.db, from, to)
	if err != nil {
		return v, err
	}
	last, err := store.LastSpeedTestTime(ctx, s.db)
	if err != nil {
		return v, err
	}
	st := s.opts.Service.State()
	v.Shown = st.Speed != nil || !last.IsZero()
	v.Source = "Speedtest Tracker"
	if st.Values.String(settings.SpeedSource) == settings.SpeedUniFi {
		v.Source = "la passerelle UniFi"
	}
	down, up, low, ok := speedStats(tests)
	if !ok {
		v.LastEver = last
		return v, nil
	}
	v.Count = len(tests)
	v.Down, v.Up, v.DownLow = mbit(down), mbit(up), mbit(low)
	t := tests[len(tests)-1]
	v.Last, v.LastDown, v.LastUp = t.Time, mbit(t.Download), mbit(t.Upload)
	var ds, us []Sample
	for i, t := range tests {
		if i > 0 && t.Time.Sub(tests[i-1].Time) > speedGap {
			gap := Sample{Time: tests[i-1].Time.Add(speedGap / 2)}
			ds, us = append(ds, gap), append(us, gap)
		}
		d, u := t.Download, t.Upload
		ds, us = append(ds, Sample{Time: t.Time, Value: &d}), append(us, Sample{Time: t.Time, Value: &u})
	}
	v.Charts = []template.HTML{
		template.HTML(SVG(Series{Label: "Débit descendant", Points: ds, Unit: "Mbit/s", ZeroBased: true})),
		template.HTML(SVG(Series{Label: "Débit montant", Points: us, Unit: "Mbit/s", ZeroBased: true})),
	}
	return v, nil
}
