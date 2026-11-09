package webview

import (
	"fmt"
	"html/template"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/events"
	"github.com/floopich/mire/internal/probe"
	"github.com/floopich/mire/internal/store"
)

// La chronologie aligne sur une meme periode tout ce que Mire mesure : etat
// de la ligne, coupures et evenements, signal, erreurs, Internet, debit,
// temperature. Les bandes partagent le meme axe du temps : ce qui arrive en
// meme temps se lit d'un regard. Un clic ouvre le releve de ce moment.

type lane struct {
	Title, Hint string
	Chart       template.HTML
	// Legend nomme les couleurs de la bande, quand elle en a plusieurs.
	Legend []legendItem
}

type legendItem struct{ Class, Label string }

type chronoView struct {
	common
	HasData bool
	Lanes   []lane
}

// stripWide et stripNarrow sont les cadres des bandes de reperes, de memes
// marges que les courbes.
var (
	stripWide   = frame{width: 720, height: 84, padLeft: 52, padRight: 14, padTop: 8, padBottom: 24, class: "chart wide lane strip"}
	stripNarrow = frame{width: 360, height: 84, padLeft: 40, padRight: 8, padTop: 8, padBottom: 22, class: "chart narrow lane strip"}
)

// gapAfter est l'ecart entre deux releves au-dela duquel la courbe
// s'interrompt : la collecte etait arretee.
func gapAfter(interval time.Duration) time.Duration { return max(3*interval, 15*time.Minute) }

func (s *Server) handleChronology(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st := s.opts.Service.State()
	v := chronoView{common: s.commonFor(r, "Chronologie")}
	from, to := v.Period.From, v.Period.To
	link := releveLink(to.Sub(from))

	points, err := store.LinePoints(ctx, s.db, from, to)
	if err != nil {
		s.fail(w, err)
		return
	}
	samples, err := store.ProbeSamples(ctx, s.db, from, to)
	if err != nil {
		s.fail(w, err)
		return
	}
	v.HasData = len(points) > 0 || len(samples) > 0
	if !v.HasData {
		s.render(w, http.StatusOK, "chronologie", v)
		return
	}
	gap := gapAfter(st.Interval)
	t := st.Profile.Thresholds
	cal := st.Profile.Calibrated

	// Etat de la ligne, d'apres les changements inscrits au journal.
	if cal && len(points) > 0 {
		chart, err := s.stateStrip(r, points, from, to, gap)
		if err != nil {
			s.fail(w, err)
			return
		}
		v.Lanes = append(v.Lanes, lane{Title: "État de la ligne", Chart: chart, Legend: []legendItem{
			{analyze.Good.String(), "Bon"}, {analyze.Warning.String(), "À surveiller"},
			{analyze.Critical.String(), "Critique"}, {analyze.Unknown.String(), "Pas de relevé"}}})
	}

	// Coupures, micro-coupures et evenements.
	outages, err := store.Outages(ctx, s.db, from, to)
	if err != nil {
		s.fail(w, err)
		return
	}
	micro, err := store.MicroCuts(ctx, s.db, from, to)
	if err != nil {
		s.fail(w, err)
		return
	}
	evs, err := store.Events(ctx, s.db, store.EventFilter{From: from, To: to, Limit: 500})
	if err != nil {
		s.fail(w, err)
		return
	}
	v.Lanes = append(v.Lanes, lane{Title: "Coupures et événements", Chart: marksStrip(outages, micro, evs, from, to),
		Hint:   "En haut les coupures, au milieu les micro-coupures, en bas les événements du journal.",
		Legend: []legendItem{{"critique", "Ligne ou Internet"}, {"avertissement", "Réseau local"}, {"note", "Note"}}})

	if len(points) > 0 {
		line := func(val func(store.LinePoint) *float64) []Sample {
			out := make([]Sample, 0, len(points))
			for i, p := range points {
				if i > 0 && p.Time.Sub(points[i-1].Time) > gap {
					out = append(out, Sample{Time: points[i-1].Time.Add(time.Second)})
				}
				out = append(out, Sample{Time: p.Time, Value: val(p)})
			}
			return out
		}
		var band *[2]float64
		if b, ok := t.DownstreamPowerFor(t.DownstreamDefault); ok && cal {
			band = &[2]float64{b.Good.Min, b.Good.Max}
		}
		v.Lanes = append(v.Lanes, lane{Title: "Puissance descendante", Hint: "La plus basse et la plus haute des canaux, en dBmV.",
			Chart: template.HTML(SVG(Series{Label: "Puissance descendante", Unit: "dBmV", Band: band, Lane: true, From: from, To: to, Link: link,
				Points: line(func(p store.LinePoint) *float64 { return p.DSPowerMin }),
				Second: line(func(p store.LinePoint) *float64 { return p.DSPowerMax })}))})

		var floor *float64
		if b, ok := t.SNRFor(t.SNRDefault); ok && cal {
			f := b.GoodMin
			floor = &f
		}
		v.Lanes = append(v.Lanes, lane{Title: "SNR le plus bas", Hint: "En dB, tous canaux descendants confondus.",
			Chart: template.HTML(SVG(Series{Label: "SNR le plus bas", Unit: "dB", Floor: floor, Lane: true, From: from, To: to, Link: link,
				Points: line(func(p store.LinePoint) *float64 { return p.SNRMin })}))})

		var usBand *[2]float64
		if b, ok := t.UpstreamPowerFor(t.UpstreamDefault); ok && cal {
			usBand = &[2]float64{b.Good.Min, b.Good.Max}
		}
		v.Lanes = append(v.Lanes, lane{Title: "Émission la plus forte", Hint: "En dBmV, voie montante.",
			Chart: template.HTML(SVG(Series{Label: "Émission la plus forte", Unit: "dBmV", Band: usBand, Lane: true, From: from, To: to, Link: link,
				Points: line(func(p store.LinePoint) *float64 { return p.USPowerMax })}))})

		var errs []store.ErrorPoint
		for _, p := range points {
			if p.Known {
				errs = append(errs, store.ErrorPoint{Timestamp: p.Time, Uncorrected: p.Uncorrected})
			}
		}
		v.Lanes = append(v.Lanes, lane{Title: "Erreurs non corrigées", Hint: "Toute la ligne, par tranche.",
			Chart: template.HTML(errorBarsFrames("Erreurs non corrigées", errs, from, to, laneWide, laneNarrow))})
	}

	if len(samples) > 0 {
		var lat, jit []Sample
		const bucket = 5 * time.Minute
		for i, sm := range samples {
			if i > 0 && sm.Start.Sub(samples[i-1].Start) > 2*bucket {
				lat = append(lat, Sample{Time: samples[i-1].Start.Add(bucket)})
				jit = append(jit, Sample{Time: samples[i-1].Start.Add(bucket)})
			}
			at := sm.Start.Add(bucket / 2)
			lat = append(lat, Sample{Time: at, Value: sm.RTTAvg})
			jit = append(jit, Sample{Time: at, Value: sm.Jitter})
		}
		v.Lanes = append(v.Lanes, lane{Title: "Latence et gigue", Hint: "En millisecondes.",
			Chart:  template.HTML(SVG(Series{Label: "Latence et gigue", Unit: "ms", ZeroBased: true, Lane: true, From: from, To: to, Points: lat, Second: jit})),
			Legend: []legendItem{{"line", "Latence"}, {"line second", "Gigue"}}})
	}

	tests, err := store.SpeedTests(ctx, s.db, from, to)
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(tests) > 1 {
		var down, up []Sample
		for _, ts := range tests {
			d, u := ts.Download, ts.Upload
			down = append(down, Sample{Time: ts.Time, Value: &d})
			up = append(up, Sample{Time: ts.Time, Value: &u})
		}
		v.Lanes = append(v.Lanes, lane{Title: "Débit", Hint: "En Mbit/s, mesuré hors de Mire.",
			Chart:  template.HTML(SVG(Series{Label: "Débit", Unit: "Mbit/s", ZeroBased: true, Lane: true, From: from, To: to, Points: down, Second: up})),
			Legend: []legendItem{{"line", "Descendant"}, {"line second", "Montant"}}})
	}

	days, err := store.Weather(ctx, s.db, from.Local().Format(time.DateOnly), to.Local().Format(time.DateOnly))
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(days) > 1 {
		var temp []Sample
		for _, d := range days {
			if day, err := time.ParseInLocation(time.DateOnly, d.Day, time.Local); err == nil {
				temp = append(temp, Sample{Time: day.Add(12 * time.Hour), Value: d.Mean})
			}
		}
		v.Lanes = append(v.Lanes, lane{Title: "Température extérieure", Hint: "Moyenne du jour, en °C.",
			Chart: template.HTML(SVG(Series{Label: "Température extérieure", Unit: "°C", Lane: true, From: from, To: to, Points: temp}))})
	}
	s.render(w, http.StatusOK, "chronologie", v)
}

// releveLink rend le lien vers le releve d'un instant.
func releveLink(span time.Duration) func(time.Time) (string, string) {
	return func(at time.Time) (string, string) {
		return "/releve?" + url.Values{"t": {at.Local().Format(finLayout)}}.Encode(),
			"Relevé de " + timeTick(at, span) + " : cliquer pour l'ouvrir"
	}
}

// stateStrip dessine l'etat de la ligne : le dernier etat inscrit avant la
// periode, puis chaque changement. Sans releve, l'etat est inconnu.
func (s *Server) stateStrip(r *http.Request, points []store.LinePoint, from, to time.Time, gap time.Duration) (template.HTML, error) {
	ctx := r.Context()
	level := analyze.Good // sans changement inscrit, la ligne n'a jamais ete en defaut
	if l, ok, err := store.StateBefore(ctx, s.db, events.KindState, from); err != nil {
		return "", err
	} else if ok {
		level = analyze.Level(l)
	}
	changes, err := store.Events(ctx, s.db, store.EventFilter{From: from, To: to, Limit: 1000})
	if err != nil {
		return "", err
	}
	type seg struct {
		from, to time.Time
		level    analyze.Level
	}
	var segs []seg
	// Les periodes couvertes par des releves, interrompues par les trous.
	start := points[0].Time
	if start.Sub(from) <= gap {
		start = from // le releve d'avant la periode la couvre
	}
	for i := 1; i <= len(points); i++ {
		if i == len(points) || points[i].Time.Sub(points[i-1].Time) > gap {
			end := points[i-1].Time
			if i == len(points) && to.Sub(end) <= gap {
				end = to
			}
			segs = append(segs, seg{from: start, to: end})
			if i < len(points) {
				start = points[i].Time
			}
		}
	}
	// Les changements d'etat, du plus ancien au plus recent.
	var marks []store.Event
	for i := len(changes) - 1; i >= 0; i-- {
		if changes[i].Kind == events.KindState {
			marks = append(marks, changes[i])
		}
	}
	draw := func(f frame) string {
		var b strings.Builder
		fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" role="img" aria-label="État de la ligne" class="%s">`, f.width, 40, f.class+" state")
		t0, t1 := span(from, to)
		x := func(t time.Time) float64 { return f.x(t.Unix(), t0, t1) }
		// Fond : pas de releve.
		fmt.Fprintf(&b, `<rect x="%d" y="6" width="%.1f" height="20" class="inconnu"/>`, f.padLeft, x(to)-float64(f.padLeft))
		paint := func(a, z time.Time, l analyze.Level) {
			if !z.After(a) {
				return
			}
			fmt.Fprintf(&b, `<rect x="%.1f" y="6" width="%.1f" height="20" class="%s"><title>%s depuis %s</title></rect>`,
				x(a), math.Max(x(z)-x(a), 1), l, Label(l), timeTick(a, to.Sub(from)))
		}
		cur, mi := alertLevel(level), 0
		for _, sg := range segs {
			for mi < len(marks) && !marks[mi].Time.After(sg.from) {
				cur = alertLevel(analyze.Level(marks[mi].Level))
				mi++
			}
			t := sg.from
			for mi < len(marks) && marks[mi].Time.Before(sg.to) {
				paint(t, marks[mi].Time, cur)
				t, cur = marks[mi].Time, alertLevel(analyze.Level(marks[mi].Level))
				mi++
			}
			paint(t, sg.to, cur)
		}
		b.WriteString(`</svg>`)
		return b.String()
	}
	return template.HTML(draw(stripWide) + draw(stripNarrow)), nil
}

// alertLevel ramene un niveau aux etats du journal : un etat tolere est bon.
func alertLevel(l analyze.Level) analyze.Level {
	if l == analyze.Tolerated {
		return analyze.Good
	}
	return l
}

// marksStrip dessine les coupures, les micro-coupures et les evenements.
func marksStrip(outages []store.Outage, micro []store.MicroCut, evs []store.Event, from, to time.Time) template.HTML {
	draw := func(f frame) string {
		var b strings.Builder
		fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" role="img" aria-label="Coupures et événements" class="%s">`, f.width, f.height, f.class)
		t0, t1 := span(from, to)
		x := func(t time.Time) float64 {
			if t.Before(from) {
				t = from
			}
			if t.After(to) {
				t = to
			}
			return f.x(t.Unix(), t0, t1)
		}
		right := float64(f.width - f.padRight)
		for _, y := range []int{18, 38, 58} {
			fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%.1f" y2="%d" class="grid"/>`, f.padLeft, y, right, y)
		}
		period := to.Sub(from)
		for _, o := range outages {
			end := o.End
			if o.Ongoing() {
				end = to
			}
			class := "critique"
			if o.Kind == probe.KindLocal {
				class = "avertissement"
			}
			fmt.Fprintf(&b, `<rect x="%.1f" y="11" width="%.1f" height="14" rx="3" class="%s"><title>%s : %s (%s)</title></rect>`,
				x(o.Start), math.Max(x(end)-x(o.Start), 3), class, timeTick(o.Start, period), escape(strings.TrimSuffix(o.Detail, ".")), events.Duration(o.Duration(end)))
		}
		for _, m := range micro {
			class := "critique"
			if m.Kind == probe.KindLocal {
				class = "avertissement"
			}
			fmt.Fprintf(&b, `<rect x="%.1f" y="32" width="2" height="12" class="%s"><title>%s : micro-coupure, %s</title></rect>`,
				x(m.Start)-1, class, timeTick(m.Start, period), microSpan(m.Min, m.Max))
		}
		for _, e := range evs {
			class := analyze.Level(e.Level).String()
			if e.Kind == store.NoteKind {
				class = "note"
			}
			fmt.Fprintf(&b, `<circle cx="%.1f" cy="58" r="5" class="%s"><title>%s : %s</title></circle>`,
				x(e.Time), class, timeTick(e.Time, period), escape(e.Message))
		}
		timeAxis(&b, f, t0, t1, f.height-6)
		b.WriteString(`</svg>`)
		return b.String()
	}
	return template.HTML(draw(stripWide) + draw(stripNarrow))
}
