package webview

import (
	"fmt"
	"html/template"
	"math"
	"strings"
	"time"
)

// progressView est l'attente du prochain releve, en indicateur ondule.
type progressView struct {
	SVG   template.HTML
	Label string
}

const (
	waveWidth  = 600.0
	waveHeight = 14.0
	waveAmp    = 3.0
	waveLength = 22.0
	waveGap    = 8.0
)

// nextReading dessine le temps ecoule depuis le dernier releve.
//
// La page ne s'anime pas : l'indicateur est juste a l'instant du rendu, et la
// page se recharge d'elle-meme a l'arrivee du releve suivant.
func nextReading(last, now time.Time, interval time.Duration) *progressView {
	if last.IsZero() || interval <= 0 {
		return nil
	}
	elapsed := now.Sub(last)
	if elapsed < 0 || elapsed > interval+interval/2 {
		return nil
	}
	frac := math.Min(1, float64(elapsed)/float64(interval))
	remaining := interval - elapsed
	label := "Prochain relevé imminent"
	if remaining >= time.Minute {
		label = fmt.Sprintf("Prochain relevé dans environ %d%smin", int(math.Ceil(remaining.Minutes())), nbsp)
	}

	end := waveWidth * frac
	mid := waveHeight / 2
	var wave strings.Builder
	fmt.Fprintf(&wave, "M2 %.1f", mid)
	for x := 2.0; x <= end-2; x += 2 {
		y := mid + waveAmp*math.Sin(2*math.Pi*x/waveLength)
		fmt.Fprintf(&wave, "L%.1f %.2f", x, y)
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="wavy" viewBox="0 0 %g %g" role="progressbar" aria-valuemin="0" aria-valuemax="100" aria-valuenow="%d" aria-label="%s">`,
		waveWidth, waveHeight, int(frac*100), escape(label))
	if end > 4 {
		fmt.Fprintf(&b, `<path d="%s" class="wave-done"/>`, wave.String())
	}
	if trackStart := end + waveGap; trackStart < waveWidth-4 {
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" class="wave-track"/>`, trackStart, mid, waveWidth-2, mid)
		fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="2" class="wave-stop"/>`, waveWidth-2, mid)
	}
	b.WriteString(`</svg>`)
	return &progressView{SVG: template.HTML(b.String()), Label: label}
}
