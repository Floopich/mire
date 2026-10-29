package webview

import (
	"fmt"
	"html/template"
	"math"
	"sort"
	"strings"

	"github.com/floopich/mire/internal/analyze"
)

const (
	specHeight = 64
	specBarTop = 4
	specBarH   = 56
	specPad    = 5
	// Au-dela de cet ecart, le spectre est raccourci : la plage vide entre
	// les canaux SC-QAM et le bloc OFDM ecraserait sinon tous les canaux.
	specGapMHz     = 48.0
	specGapShownAs = 20.0
)

// spectrumView est la bande de canaux d'une voie, prete a afficher.
type spectrumView struct {
	Wide   template.HTML
	Narrow template.HTML
	From   string
	To     string
	Breaks bool
}

// Spectrum dessine la bande de canaux : chaque canal a sa frequence, colore
// selon son etat.
//
// C'est la lecture d'ensemble de la ligne, celle qu'une mire de reglage donne
// d'un coup d'oeil : un canal en defaut se voit a sa place dans le spectre,
// sans parcourir un tableau. Les canaux dont le modem ne donne pas la
// frequence restent dans le tableau mais pas ici, faute de position honnete.
func Spectrum(label string, rows []channelRow, wide, narrow int) *spectrumView {
	var placed []channelRow
	for _, r := range rows {
		if r.Freq != nil {
			placed = append(placed, r)
		}
	}
	if len(placed) == 0 {
		return nil
	}
	sort.Slice(placed, func(i, j int) bool { return *placed[i].Freq < *placed[j].Freq })

	// Position de chaque canal sur un axe ou les grands vides sont raccourcis.
	pos := make([]float64, len(placed))
	var breaks []float64
	for i := 1; i < len(placed); i++ {
		gap := *placed[i].Freq - *placed[i-1].Freq
		if gap > specGapMHz {
			breaks = append(breaks, pos[i-1]+specGapShownAs/2)
			gap = specGapShownAs
		}
		pos[i] = pos[i-1] + gap
	}
	span := pos[len(pos)-1]
	if span < 16 {
		span = 16
	}

	v := &spectrumView{
		From:   FmtMHz(placed[0].Freq),
		To:     FmtMHz(placed[len(placed)-1].Freq),
		Breaks: len(breaks) > 0,
	}
	v.Wide = template.HTML(drawSpectrum(label, placed, pos, breaks, span, wide, "spectrum wide"))
	if narrow > 0 {
		v.Narrow = template.HTML(drawSpectrum(label, placed, pos, breaks, span, narrow, "spectrum narrow"))
	}
	return v
}

func drawSpectrum(label string, placed []channelRow, pos, breaks []float64, span float64, width int, class string) string {
	plot := float64(width - 2*specPad)
	// Un canal SC-QAM occupe 8 MHz en Europe : la barre n'est jamais plus
	// large que sa place reelle, ni plus fine que lisible.
	perMHz := plot / (span + 8)
	barW := math.Max(2.5, math.Min(12, math.Min(8*perMHz*0.78, plot/float64(len(placed))*0.8)))
	offset := float64(specPad) + 4*perMHz

	counts := map[analyze.Level]int{}
	for _, r := range placed {
		counts[r.Level]++
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" role="img" aria-label="%s : %s" class="%s">`,
		width, specHeight, escape(label), escape(summary(len(placed), counts)), class)
	fmt.Fprintf(&b, `<rect x="0" y="0" width="%d" height="%d" rx="16" class="spec-bg"/>`, width, specHeight)
	for _, br := range breaks {
		x := offset + br*perMHz
		fmt.Fprintf(&b, `<path d="M%.1f %d l-3 %d M%.1f %d l-3 %d" class="spec-break"/>`,
			x-1.5, specBarTop+specBarH-14, 10, x+2.5, specBarTop+specBarH-14, 10)
	}
	for i, r := range placed {
		x := offset + pos[i]*perMHz - barW/2
		kind := ""
		if r.Wide {
			kind = " wide"
		}
		fmt.Fprintf(&b, `<a href="%s"><rect x="%.1f" y="%d" width="%.1f" height="%d" rx="%.1f" class="bar %s%s"><title>Canal %d, %s, %s : %s</title></rect></a>`,
			escape(r.Href), x, specBarTop, barW, specBarH, barW/2, r.Level.String(), kind,
			r.ID, FmtMHz(r.Freq), escape(r.Modulation), escape(Label(r.Level)))
	}
	b.WriteString(`</svg>`)
	return b.String()
}

// summary resume la bande en une phrase, pour les lecteurs d'ecran.
func summary(total int, counts map[analyze.Level]int) string {
	parts := []string{fmt.Sprintf("%d canaux", total)}
	for _, l := range []analyze.Level{analyze.Good, analyze.Tolerated, analyze.Warning, analyze.Critical, analyze.Unknown} {
		if n := counts[l]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, strings.ToLower(Label(l))))
		}
	}
	return strings.Join(parts, ", ")
}
