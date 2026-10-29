package webview

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/floopich/mire/internal/store"
)

// Le profil horaire replie trente jours de mesures de la sonde sur une
// journee : latence et pertes moyennes pour chaque heure.

const (
	// profileDays est la profondeur du profil.
	profileDays = 30
	// profileMinDays est le nombre de jours mesures qu'il faut a une heure
	// pour compter : une seule soiree ne fait pas une habitude.
	profileMinDays = 3
)

// hourStat est une heure de la journee, toutes journees confondues.
type hourStat struct {
	Days    int      // jours ou cette heure a ete mesuree
	Latency *float64 // millisecondes, moyenne ponderee par les reponses
	Loss    float64  // pour cent de tentatives sans reponse
	Rounds  int
}

// hourlyProfile agrege les tranches de la sonde par heure locale.
func hourlyProfile(samples []store.ProbeSample, loc *time.Location) [24]hourStat {
	var out [24]hourStat
	var sum [24]float64
	var answered, failed [24]int
	days := [24]map[string]bool{}
	for _, s := range samples {
		t := s.Start.In(loc)
		h := t.Hour()
		if days[h] == nil {
			days[h] = map[string]bool{}
		}
		days[h][t.Format("2006-01-02")] = true
		out[h].Rounds += s.Rounds
		failed[h] += s.Failed
		if s.RTTAvg != nil {
			n := s.Rounds - s.Failed
			sum[h] += *s.RTTAvg * float64(n)
			answered[h] += n
		}
	}
	for h := range out {
		out[h].Days = len(days[h])
		if out[h].Days < profileMinDays {
			out[h] = hourStat{Days: out[h].Days}
			continue
		}
		if answered[h] > 0 {
			v := sum[h] / float64(answered[h])
			out[h].Latency = &v
		}
		if out[h].Rounds > 0 {
			out[h].Loss = 100 * float64(failed[h]) / float64(out[h].Rounds)
		}
	}
	return out
}

// usualLatency est la latence mediane des heures mesurees ; ok est faux
// tant que trop peu d'heures le sont pour juger.
func usualLatency(p [24]hourStat) (float64, bool) {
	var lat []float64
	for _, h := range p {
		if h.Latency != nil {
			lat = append(lat, *h.Latency)
		}
	}
	if len(lat) < 18 {
		return 0, false
	}
	sort.Float64s(lat)
	return lat[len(lat)/2], true
}

// busyHour dit si une heure est chargee : nettement au-dessus de
// l'habitude, en millisecondes comme en proportion, pour ne pas s'alarmer
// de 2 ms.
func busyHour(h hourStat, usual float64) bool {
	return h.Latency != nil && *h.Latency >= usual*1.3 && *h.Latency-usual >= 5
}

// profileVerdict dit en une phrase si la connexion change selon l'heure, et
// si c'est un probleme. La phrase est vide tant que le profil est incomplet.
func profileVerdict(p [24]hourStat) (string, bool) {
	usual, ok := usualLatency(p)
	if !ok {
		return "", false
	}
	busy := func(h hourStat) bool { return busyHour(h, usual) }
	peak, peakHour := 0.0, -1
	for i, h := range p {
		if busy(h) && *h.Latency > peak {
			peak, peakHour = *h.Latency, i
		}
	}
	lossy := -1
	for i, h := range p {
		if h.Latency != nil && h.Loss >= 1 && (lossy < 0 || h.Loss > p[lossy].Loss) {
			lossy = i
		}
	}

	switch {
	case peakHour >= 0:
		first, last := peakHour, peakHour
		for first-1 >= 0 && busy(p[first-1]) {
			first--
		}
		for last+1 < 24 && busy(p[last+1]) {
			last++
		}
		when := fmt.Sprintf("vers %d h", peakHour)
		if last > first {
			when = fmt.Sprintf("entre %d h et %d h", first, last+1)
		}
		msg := fmt.Sprintf("Latence plus haute aux heures chargées : jusqu'à %s %s, contre %s d'habitude.", ms(peak), when, ms(usual))
		if lossy >= 0 {
			msg += fmt.Sprintf(" Jusqu'à %s sans réponse vers %d h.", percent(p[lossy].Loss, 1), lossy)
		}
		return msg, true
	case lossy >= 0:
		return fmt.Sprintf("Latence stable, mais jusqu'à %s sans réponse vers %d h.", percent(p[lossy].Loss, 1), lossy), true
	default:
		return fmt.Sprintf("Latence stable à toute heure, autour de %s.", ms(usual)), false
	}
}

func ms(v float64) string { return strconv.Itoa(int(math.Round(v))) + nbsp + "ms" }

// HourBars dessine une valeur par heure de la journee.
// Les heures que mark designe sont mises en evidence.
func HourBars(label string, p [24]hourStat, value func(hourStat) (float64, bool), title func(int, float64) string,
	tick func(float64) string, empty string, mark func(int) bool, class string) string {
	bars := make([]bar, 24)
	for h, st := range p {
		v, ok := value(st)
		if !ok {
			continue
		}
		bars[h] = bar{seen: true, high: v, title: title(h, v)}
		if mark != nil && mark(h) {
			bars[h].class = "bar-heure chargee"
		}
	}
	style := barStyle{classes: [2]string{"", class}, tick: tick, empty: empty}
	axisFor := func(every int) []axisTick {
		var ticks []axisTick
		for h := 0; h < 24; h += every {
			ticks = append(ticks, axisTick{at: (float64(h) + 0.5) / 24, text: strconv.Itoa(h) + nbsp + "h"})
		}
		return ticks
	}
	wide, narrow := style, style
	wide.axis, narrow.axis = axisFor(3), axisFor(6)
	// Les reperes portent leur unite (« 40 ms ») : le cadre etroit leur
	// laisse plus de place que d'habitude.
	slim := narrowFrame
	slim.padLeft = 52
	var zero time.Time
	return renderBars(label, bars, zero, zero, wideFrame, wide) + renderBars(label, bars, zero, zero, slim, narrow)
}
