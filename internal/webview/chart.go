// Package webview rend les pages de Mire. Les graphiques sont du SVG
// assemble cote serveur, sans JavaScript.
package webview

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/floopich/mire/internal/store"
)

// frame decrit le cadre d'un graphique. Chaque graphique est rendu en deux
// cadres, large et etroit ; la feuille de style montre celui qui convient.
type frame struct {
	width, height                        int
	padLeft, padRight, padTop, padBottom int
	class                                string
}

var (
	wideFrame   = frame{width: 720, height: 240, padLeft: 52, padRight: 14, padTop: 14, padBottom: 30, class: "chart wide"}
	narrowFrame = frame{width: 360, height: 200, padLeft: 40, padRight: 8, padTop: 12, padBottom: 26, class: "chart narrow"}
	// Cadres plus bas, pour les bandes de la chronologie : memes marges, pour
	// que les bandes empilees partagent le meme axe du temps.
	laneWide   = frame{width: 720, height: 150, padLeft: 52, padRight: 14, padTop: 10, padBottom: 26, class: "chart wide lane"}
	laneNarrow = frame{width: 360, height: 140, padLeft: 40, padRight: 8, padTop: 10, padBottom: 24, class: "chart narrow lane"}
)

// Sample est un point d'une courbe ; Value nil marque un trou de mesure.
type Sample struct {
	Time  time.Time
	Value *float64
}

// Samples extrait une courbe des releves d'un canal : puissance ou SNR.
func Samples(points []store.Point, value func(store.Point) *float64) []Sample {
	out := make([]Sample, len(points))
	for i, p := range points {
		out[i] = Sample{Time: p.Timestamp, Value: value(p)}
	}
	return out
}

type Series struct {
	Label  string
	Points []Sample
	Unit   string
	// ZeroBased fait partir l'echelle de zero : une latence ou un taux de
	// perte n'a pas de sens en dessous.
	ZeroBased bool
	// Band est la plage nominale de l'operateur, dessinee derriere la courbe.
	Band *[2]float64
	// Floor est un plancher : tout ce qui passe dessous est hors norme.
	Floor *float64
	// Second est une seconde courbe, dessinee plus discretement sur la meme
	// echelle : la puissance la plus haute sous la plus basse, la gigue sous
	// la latence.
	Second []Sample
	// From et To fixent l'axe du temps ; sans eux, il va du premier au
	// dernier point.
	From, To time.Time
	// Lane dessine un graphique bas, pour la chronologie.
	Lane bool
	// Link rend le lien d'une tranche de temps : un clic sur le graphique
	// ouvre le releve de ce moment.
	Link func(at time.Time) (href, title string)
}

// SVG assemble un graphique. Les points sans valeur interrompent le trace.
func SVG(s Series) string {
	if s.Lane {
		return draw(s, laneWide) + draw(s, laneNarrow)
	}
	return draw(s, wideFrame) + draw(s, narrowFrame)
}

func draw(s Series, f frame) string {
	var b strings.Builder
	// Avec des zones cliquables, l'image est un groupe a l'interieur du
	// graphique, et les zones restent a cote : une image ne peut pas
	// contenir de liens.
	if s.Link != nil {
		fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" role="none" class="%s"><g role="img" aria-label="%s">`,
			f.width, f.height, f.class, escape(s.Label))
	} else {
		fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" role="img" aria-label="%s" class="%s">`,
			f.width, f.height, escape(s.Label), f.class)
	}
	end := func() {
		if s.Link != nil {
			b.WriteString(`</g>`)
		}
	}

	known := knownValues(s)
	for _, p := range s.Second {
		if p.Value != nil {
			known = append(known, *p.Value)
		}
	}
	if len(known) < 2 {
		fmt.Fprintf(&b, `<text x="%d" y="%d" class="empty" text-anchor="middle">Pas encore assez de mesures.</text>`,
			f.width/2, f.height/2)
		end()
		b.WriteString(`</svg>`)
		return b.String()
	}

	// L'echelle englobe la plage nominale : sans elle, une courbe saine qui
	// occupe tout le cadre aurait l'air aussi agitee qu'une courbe en defaut.
	extra := known
	if s.Band != nil {
		extra = append(extra, s.Band[0], s.Band[1])
	}
	if s.Floor != nil {
		extra = append(extra, *s.Floor)
	}
	lo, hi := bounds(extra)
	if s.ZeroBased {
		lo = 0
	}
	lo, hi, step := niceScale(lo, hi)
	var t0, t1 int64
	switch {
	case !s.From.IsZero() && !s.To.IsZero():
		t0, t1 = s.From.Unix(), s.To.Unix()
	case len(s.Points) > 0:
		t0, t1 = s.Points[0].Time.Unix(), s.Points[len(s.Points)-1].Time.Unix()
	default:
		t0, t1 = s.Second[0].Time.Unix(), s.Second[len(s.Second)-1].Time.Unix()
	}
	if t1 == t0 {
		t1 = t0 + 1
	}
	right := float64(f.width - f.padRight)

	if s.Band != nil {
		yTop, yBottom := f.y(s.Band[1], lo, hi), f.y(s.Band[0], lo, hi)
		fmt.Fprintf(&b, `<rect x="%d" y="%.1f" width="%.1f" height="%.1f" class="band"><title>Plage nominale : %s à %s %s</title></rect>`,
			f.padLeft, yTop, right-float64(f.padLeft), yBottom-yTop, dec(s.Band[0]), dec(s.Band[1]), escape(s.Unit))
	}
	if s.Floor != nil {
		y := f.y(*s.Floor, lo, hi)
		fmt.Fprintf(&b, `<rect x="%d" y="%.1f" width="%.1f" height="%.1f" class="below"/>`+
			`<line x1="%d" y1="%.1f" x2="%.1f" y2="%.1f" class="floor"><title>Seuil nominal : %s %s</title></line>`,
			f.padLeft, y, right-float64(f.padLeft), float64(f.height-f.padBottom)-y,
			f.padLeft, y, right, y, dec(*s.Floor), escape(s.Unit))
	}

	// Des reperes ronds, de trois a cinq : 0, 5, 10 plutot que 0,0, 4,8, 9,6.
	for v := lo; v <= hi+step/2; v += step {
		y := f.y(v, lo, hi)
		fmt.Fprintf(&b, `<line x1="%d" y1="%.1f" x2="%.1f" y2="%.1f" class="grid"/>`, f.padLeft, y, right, y)
		fmt.Fprintf(&b, `<text x="%d" y="%.1f" class="tick" text-anchor="end">%s</text>`, f.padLeft-7, y+4, tickText(v, step))
	}

	trace := func(points []Sample, class string) {
		var path strings.Builder
		pen := "M"
		for _, p := range points {
			v := p.Value
			if v == nil {
				pen = "M" // trou de mesure : on leve le crayon
				continue
			}
			fmt.Fprintf(&path, "%s%.1f %.1f ", pen, f.x(p.Time.Unix(), t0, t1), f.y(*v, lo, hi))
			pen = "L"
		}
		if path.Len() > 0 {
			fmt.Fprintf(&b, `<path d="%s" class="%s"/>`, strings.TrimSpace(path.String()), class)
		}
	}
	trace(s.Second, "line second")
	trace(s.Points, "line")

	timeAxis(&b, f, t0, t1, f.height-8)
	end()
	hits(&b, f, t0, t1, s.Link)
	b.WriteString(`</svg>`)
	return b.String()
}

// hits pose des zones cliquables sur toute la hauteur du graphique, une par
// tranche de temps : un clic ouvre le releve de ce moment. Elles restent
// hors du parcours au clavier et des lecteurs d'ecran : 48 arrets par courbe
// noieraient la page, et la page Historique mene aussi aux releves.
func hits(b *strings.Builder, f frame, t0, t1 int64, link func(time.Time) (string, string)) {
	if link == nil {
		return
	}
	n := 48
	if f.width < 500 {
		n = 24
	}
	w := float64(f.width-f.padLeft-f.padRight) / float64(n)
	for i := range n {
		at := time.Unix(t0+(t1-t0)*int64(2*i+1)/int64(2*n), 0)
		href, title := link(at)
		if href == "" {
			continue
		}
		fmt.Fprintf(b, `<a href="%s" tabindex="-1" aria-hidden="true"><rect x="%.1f" y="%d" width="%.1f" height="%d" class="hit"><title>%s</title></rect></a>`,
			escape(href), float64(f.padLeft)+w*float64(i), f.padTop, w, f.height-f.padTop-f.padBottom, escape(title))
	}
}

// timeAxis ecrit les reperes du temps sous un graphique : deux a quatre
// instants ronds (21:00, 09:00, minuit, le 3 novembre) plutot que le debut,
// le milieu et la fin de la periode, qui tombent a 21:27 ou 09:25 ; le jour
// est precise quand il n'est pas celui de la fin.
func timeAxis(b *strings.Builder, f frame, t0, t1 int64, y int) {
	if t1 <= t0 {
		return
	}
	span := time.Duration(t1-t0) * time.Second
	left, right := float64(f.padLeft), float64(f.width-f.padRight)
	for _, ts := range axisTicks(t0, t1) {
		x := f.x(ts, t0, t1)
		anchor := "middle"
		switch {
		case x-left < (right-left)*0.08:
			anchor = "start"
		case right-x < (right-left)*0.08:
			anchor = "end"
		}
		fmt.Fprintf(b, `<text x="%.1f" y="%d" class="tick" text-anchor="%s">%s</text>`,
			x, y, anchor, escape(axisLabel(time.Unix(ts, 0), time.Unix(t1, 0), span)))
	}
}

// axisSteps sont les ecarts possibles entre deux reperes.
var axisSteps = []time.Duration{time.Hour, 2 * time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour,
	24 * time.Hour, 2 * 24 * time.Hour, 7 * 24 * time.Hour, 14 * 24 * time.Hour, 30 * 24 * time.Hour, 60 * 24 * time.Hour, 90 * 24 * time.Hour}

// axisTicks choisit le plus petit ecart qui donne au plus quatre reperes,
// alignes sur l'heure locale : multiples de l'ecart depuis minuit, ou
// minuits pour les ecarts d'un jour et plus.
func axisTicks(t0, t1 int64) []int64 {
	var out []int64
	for _, step := range axisSteps {
		out = roundTicks(time.Unix(t0, 0).Local(), time.Unix(t1, 0).Local(), step)
		if len(out) <= 4 {
			break
		}
	}
	if len(out) < 2 {
		// Periode trop courte pour deux instants ronds : le debut et la fin.
		return []int64{t0, t1}
	}
	return out
}

func roundTicks(from, to time.Time, step time.Duration) []int64 {
	midnight := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, from.Location())
	var out []int64
	if step < 24*time.Hour {
		for t := midnight; !t.After(to); t = t.Add(step) {
			if !t.Before(from) {
				out = append(out, t.Unix())
			}
		}
		return out
	}
	days := int(step / (24 * time.Hour))
	for t := midnight; !t.After(to); t = t.AddDate(0, 0, days) {
		if !t.Before(from) {
			out = append(out, t.Unix())
		}
	}
	return out
}

// axisLabel nomme un repere : l'heure sur une journee, precedee de « hier »
// ou de la date quand le jour n'est pas celui de la fin ; la date au-dela.
func axisLabel(t, end time.Time, span time.Duration) string {
	t, end = t.Local(), end.Local()
	if span > 36*time.Hour {
		return t.Format("02/01")
	}
	ty, tm, td := t.Date()
	ey, em, ed := end.Date()
	switch {
	case ty == ey && tm == em && td == ed:
		return t.Format("15:04")
	case end.AddDate(0, 0, -1).Day() == td && end.AddDate(0, 0, -1).Month() == tm:
		return "hier " + t.Format("15:04")
	}
	return t.Format("02/01 15:04")
}

// timeTick adapte la precision a la fenetre : l'heure sur une journee, la
// date au-dela.
func timeTick(t time.Time, span time.Duration) string {
	t = t.Local()
	switch {
	case span <= 36*time.Hour:
		return t.Format("15:04")
	case span <= 10*24*time.Hour:
		return t.Format("02/01 15:04")
	default:
		return t.Format("02/01")
	}
}

func knownValues(s Series) []float64 {
	var out []float64
	for _, p := range s.Points {
		if v := p.Value; v != nil {
			out = append(out, *v)
		}
	}
	return out
}

// bounds choisit l'echelle verticale : marge de 10 % et intervalle minimal.
func bounds(values []float64) (lo, hi float64) {
	lo, hi = values[0], values[0]
	for _, v := range values {
		lo = math.Min(lo, v)
		hi = math.Max(hi, v)
	}
	if hi-lo < 1 {
		mid := (lo + hi) / 2
		lo, hi = mid-0.5, mid+0.5
	}
	margin := (hi - lo) * 0.1
	return lo - margin, hi + margin
}

// niceScale arrondit l'echelle a des reperes ronds, quatre intervalles au
// plus : pas de 1, 2, 2,5 ou 5 fois une puissance de dix.
func niceScale(lo, hi float64) (float64, float64, float64) {
	if hi <= lo {
		hi = lo + 1
	}
	raw := (hi - lo) / 4
	e := math.Pow(10, math.Floor(math.Log10(raw)))
	for _, m := range []float64{1, 2, 2.5, 5, 10, 20} {
		step := m * e
		l := math.Floor(lo/step+1e-9) * step
		h := math.Ceil(hi/step-1e-9) * step
		if (h-l)/step <= 4+1e-9 {
			return l, h, step
		}
	}
	return lo, hi, (hi - lo) / 2
}

// tickText rend un repere avec juste les decimales que demande le pas.
func tickText(v, step float64) string {
	decimals := 0
	for d := step; d < 1-1e-9 && decimals < 3; d *= 10 {
		decimals++
	}
	if math.Abs(step*math.Pow(10, float64(decimals))-math.Round(step*math.Pow(10, float64(decimals)))) > 1e-6 {
		decimals++
	}
	if math.Abs(v) < step/1e6 {
		v = 0
	}
	s := strings.Replace(strconv.FormatFloat(v, 'f', decimals, 64), ".", ",", 1)
	return strings.Replace(s, "-", "\u2212", 1)
}

func (f frame) y(v, lo, hi float64) float64 {
	usable := float64(f.height - f.padTop - f.padBottom)
	return float64(f.padTop) + usable*(1-(v-lo)/(hi-lo))
}

func (f frame) x(ts, t0, t1 int64) float64 {
	return float64(f.padLeft) + float64(f.width-f.padLeft-f.padRight)*float64(ts-t0)/float64(t1-t0)
}
