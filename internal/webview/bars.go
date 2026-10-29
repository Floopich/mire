package webview

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/floopich/mire/internal/store"
)

// Les graphiques en barres regroupent les mesures en tranches de duree
// egale. Une tranche sans mesure reste vide.

// bar est une tranche : une barre basse et, empilee dessus, une barre haute.
type bar struct {
	seen      bool
	low, high float64
	title     string
	// class remplace la classe de la barre haute, pour en distinguer une.
	class string
}

type barStyle struct {
	classes [2]string
	tick    func(float64) string
	empty   string
	// axis remplace les reperes de temps sous les barres, pour un axe par
	// categories (les heures de la journee) ; ses positions vont de 0 a 1.
	axis []axisTick
}

type axisTick struct {
	at   float64
	text string
}

// slot renvoie la tranche d'un instant, ou -1 hors de la fenetre.
func slot(t time.Time, t0, t1 int64, n int) int {
	i := int(float64(t.Unix()-t0) / float64(t1-t0) * float64(n))
	if i == n {
		return n - 1 // la mesure prise pile a la fin de la fenetre
	}
	if i < 0 || i > n {
		return -1
	}
	return i
}

func span(from, to time.Time) (int64, int64) {
	t0, t1 := from.Unix(), to.Unix()
	if t1 <= t0 {
		t1 = t0 + 1
	}
	return t0, t1
}

// ErrorBars dessine les erreurs d'un canal : corrigees en bas, non corrigees
// au-dessus.
func ErrorBars(label string, points []store.ErrorPoint, from, to time.Time) string {
	build := func(n int) []bar {
		t0, t1 := span(from, to)
		bars := make([]bar, n)
		for _, p := range points {
			if i := slot(p.Timestamp, t0, t1, n); i >= 0 {
				bars[i].seen = true
				bars[i].low += float64(p.Corrected)
				bars[i].high += float64(p.Uncorrected)
			}
		}
		for i := range bars {
			start := time.Unix(t0+(t1-t0)*int64(i)/int64(n), 0)
			bars[i].title = fmt.Sprintf("%s : %s corrigées, %s non corrigées", timeTick(start, to.Sub(from)),
				FmtCount(int(bars[i].low)), FmtCount(int(bars[i].high)))
		}
		return bars
	}
	style := barStyle{classes: [2]string{"bar-corr", "bar-noncorr"}, empty: "Aucune erreur sur la période.",
		tick: func(v float64) string { return compact(int64(v)) }}
	if len(points) == 0 {
		style.empty = "Pas encore assez de relevés."
	}
	return renderBars(label, build(96), from, to, wideFrame, style) +
		renderBars(label, build(48), from, to, narrowFrame, style)
}

// LossBars dessine la part de tentatives de la sonde restees sans reponse.
func LossBars(label string, samples []store.ProbeSample, from, to time.Time) string {
	build := func(n int) []bar {
		t0, t1 := span(from, to)
		rounds := make([]int, n)
		failed := make([]int, n)
		bars := make([]bar, n)
		for _, s := range samples {
			if i := slot(s.Start, t0, t1, n); i >= 0 {
				bars[i].seen = true
				rounds[i] += s.Rounds
				failed[i] += s.Failed
			}
		}
		for i := range bars {
			if rounds[i] > 0 {
				bars[i].high = 100 * float64(failed[i]) / float64(rounds[i])
			}
			start := time.Unix(t0+(t1-t0)*int64(i)/int64(n), 0)
			bars[i].title = fmt.Sprintf("%s : %s sans réponse", timeTick(start, to.Sub(from)), percent(bars[i].high, 1))
		}
		return bars
	}
	style := barStyle{classes: [2]string{"", "bar-noncorr"}, empty: "Toutes les tentatives ont reçu une réponse.",
		tick: lossTick}
	if len(samples) == 0 {
		style.empty = "Aucune mesure sur la période."
	}
	return renderBars(label, build(96), from, to, wideFrame, style) +
		renderBars(label, build(48), from, to, narrowFrame, style)
}

// niceCeil arrondit le haut d'une echelle a une valeur ronde, dont la
// moitie l'est aussi : 1, 2, 4, 5 ou 10 fois une puissance de dix. Les
// reperes tombent alors sur 0, 20 et 40 ms plutot que sur 0, 15,5 et 31.
func niceCeil(v float64) float64 {
	if v <= 0 {
		return v
	}
	e := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 4, 5, 10} {
		if v <= m*e*(1+1e-9) {
			return m * e
		}
	}
	return 10 * e
}

// lossTick rend un repere de pertes sans zero superflu : 0 %, 0,5 %, 1 %.
// Des pertes faibles restent lisibles : 0,25 % plutot que 0 %.
func lossTick(v float64) string {
	s := strconv.FormatFloat(math.Round(v*100)/100, 'f', -1, 64)
	return strings.Replace(s, ".", ",", 1) + "\u202f%"
}

func renderBars(label string, bars []bar, from, to time.Time, f frame, st barStyle) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" role="img" aria-label="%s" class="%s">`,
		f.width, f.height, escape(label), f.class)

	var peak float64
	for _, bk := range bars {
		peak = math.Max(peak, bk.low+bk.high)
	}
	if peak == 0 {
		fmt.Fprintf(&b, `<text x="%d" y="%d" class="empty" text-anchor="middle">%s</text></svg>`,
			f.width/2, f.height/2, escape(st.empty))
		return b.String()
	}
	peak = niceCeil(peak)

	t0, t1 := span(from, to)
	right := float64(f.width - f.padRight)
	usable := float64(f.height - f.padTop - f.padBottom)
	base := float64(f.height - f.padBottom)
	y := func(v float64) float64 { return usable * v / peak }

	for i := range 3 {
		v := peak * float64(i) / 2
		yy := base - y(v)
		fmt.Fprintf(&b, `<line x1="%d" y1="%.1f" x2="%.1f" y2="%.1f" class="grid"/>`, f.padLeft, yy, right, yy)
		fmt.Fprintf(&b, `<text x="%d" y="%.1f" class="tick" text-anchor="end">%s</text>`, f.padLeft-7, yy+4, st.tick(v))
	}

	n := len(bars)
	step := (right - float64(f.padLeft)) / float64(n)
	width := step * 0.7
	for i, bk := range bars {
		if !bk.seen || bk.low+bk.high == 0 {
			continue
		}
		x := float64(f.padLeft) + step*float64(i) + (step-width)/2
		hl, hh := y(bk.low), y(bk.high)
		fmt.Fprintf(&b, `<g><title>%s</title>`, escape(bk.title))
		if bk.low > 0 {
			fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" class="%s"/>`, x, base-hl, width, hl, st.classes[0])
		}
		if bk.high > 0 {
			class := st.classes[1]
			if bk.class != "" {
				class = bk.class
			}
			fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" class="%s"/>`, x, base-hl-hh, width, hh, class)
		}
		b.WriteString(`</g>`)
	}

	if st.axis != nil {
		for _, tk := range st.axis {
			fmt.Fprintf(&b, `<text x="%.1f" y="%d" class="tick" text-anchor="middle">%s</text>`,
				float64(f.padLeft)+(right-float64(f.padLeft))*tk.at, f.height-8, escape(tk.text))
		}
		b.WriteString(`</svg>`)
		return b.String()
	}
	period := to.Sub(from)
	for i, anchor := range []string{"start", "middle", "end"} {
		ts := t0 + (t1-t0)*int64(i)/2
		fmt.Fprintf(&b, `<text x="%.1f" y="%d" class="tick" text-anchor="%s">%s</text>`,
			f.x(ts, t0, t1), f.height-8, anchor, escape(timeTick(time.Unix(ts, 0), period)))
	}
	b.WriteString(`</svg>`)
	return b.String()
}

// compact abrege un repere : 1 200 devient « 1,2 k », 3 400 000 « 3,4 M ».
func compact(v int64) string {
	switch {
	case v >= 1_000_000:
		return strings.TrimSuffix(dec(float64(v)/1e6), ",0") + nbsp + "M"
	case v >= 10_000:
		return fmt.Sprintf("%d", v/1000) + nbsp + "k"
	case v >= 1000:
		// Un repere rond le reste : « 2 k », pas « 2,0 k ».
		return strings.TrimSuffix(dec(float64(v)/1000), ",0") + nbsp + "k"
	default:
		return fmt.Sprintf("%d", v)
	}
}
