// Package webview rend les releves sous forme de pages lisibles.
//
// Les graphiques sont du SVG assemble cote serveur : aucun JavaScript, aucun
// fichier externe. La page reste lisible sur un reseau isole, et la politique
// de securite du contenu peut tout interdire sauf la feuille de style.
package webview

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/floopich/mire/internal/store"
)

// frame decrit le cadre d'un graphique.
//
// Le texte d'un SVG grandit et retrecit avec lui : un seul cadre donnerait des
// reperes illisibles sur un telephone ou enormes sur un ecran large. Chaque
// graphique est donc rendu en deux cadres, et la feuille de style montre celui
// qui convient a la largeur.
type frame struct {
	width, height                        int
	padLeft, padRight, padTop, padBottom int
	class                                string
}

var (
	wideFrame   = frame{width: 720, height: 240, padLeft: 52, padRight: 14, padTop: 14, padBottom: 30, class: "chart wide"}
	narrowFrame = frame{width: 360, height: 200, padLeft: 40, padRight: 8, padTop: 12, padBottom: 26, class: "chart narrow"}
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

// Series est une courbe a tracer.
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
}

// SVG assemble un graphique.
//
// Les points sans valeur interrompent le trace au lieu d'etre relies : une
// ligne droite au-dessus d'un trou de mesure ferait croire a une continuite
// qui n'a pas ete observee.
func SVG(s Series) string {
	return draw(s, wideFrame) + draw(s, narrowFrame)
}

func draw(s Series, f frame) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" role="img" aria-label="%s" class="%s">`,
		f.width, f.height, escape(s.Label), f.class)

	known := knownValues(s)
	if len(known) < 2 {
		fmt.Fprintf(&b, `<text x="%d" y="%d" class="empty" text-anchor="middle">Pas encore assez de mesures.</text></svg>`,
			f.width/2, f.height/2)
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
	t0 := s.Points[0].Time.Unix()
	t1 := s.Points[len(s.Points)-1].Time.Unix()
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

	var path strings.Builder
	pen := "M"
	for _, p := range s.Points {
		v := p.Value
		if v == nil {
			pen = "M" // trou de mesure : on leve le crayon
			continue
		}
		fmt.Fprintf(&path, "%s%.1f %.1f ", pen, f.x(p.Time.Unix(), t0, t1), f.y(*v, lo, hi))
		pen = "L"
	}
	fmt.Fprintf(&b, `<path d="%s" class="line"/>`, strings.TrimSpace(path.String()))

	// Trois reperes horaires : debut, milieu, fin.
	span := time.Duration(t1-t0) * time.Second
	for i, anchor := range []string{"start", "middle", "end"} {
		ts := t0 + (t1-t0)*int64(i)/2
		fmt.Fprintf(&b, `<text x="%.1f" y="%d" class="tick" text-anchor="%s">%s</text>`,
			f.x(ts, t0, t1), f.height-8, anchor, escape(timeTick(time.Unix(ts, 0), span)))
	}
	b.WriteString(`</svg>`)
	return b.String()
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

// bounds choisit l'echelle verticale.
//
// Une marge de 10 % evite que la courbe colle aux bords, et un intervalle
// plancher empeche qu'une ligne parfaitement stable soit rendue comme une
// oscillation spectaculaire entre deux valeurs identiques.
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
