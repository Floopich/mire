package webview

import (
	"html/template"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/floopich/mire/internal/settings"
	"github.com/floopich/mire/internal/store"
)

// La tendance montre l'evolution lente de la ligne : moyennes journalieres
// des canaux SC-QAM, et lien avec la temperature exterieure.

const (
	// minDays est le nombre de journees en dessous duquel une pente ou une
	// correlation ne veut rien dire.
	minDays = 14
	// stableSlope est la pente, en dB par mois, sous laquelle une mesure est
	// dite stable : moins que la precision d'affichage du modem sur un mois.
	stableSlope = 0.5
	// linked est la correlation a partir de laquelle la temperature est dite
	// liee a la mesure.
	linked = 0.5
)

var trendWindows = []int{30, 90, 365}

type trendWindow struct {
	Days   int
	Label  string
	Active bool
}

type trendFinding struct {
	Title, Text string
	Alert       bool
}

type trendChart struct {
	Title, Hint string
	// SVG est assemble par ce paquet, valeurs echappees : il est sur.
	SVG template.HTML
}

type trendView struct {
	common
	Days        int
	TrendWins   []trendWindow
	HasData     bool
	DayCount    int
	Findings    []trendFinding
	Charts      []trendChart
	WeatherOn   bool
	HasWeather  bool
	WeatherNote string
}

func (s *Server) handleTrend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st := s.opts.Service.State()
	days := 90
	if d, err := strconv.Atoi(r.URL.Query().Get("jours")); err == nil {
		for _, ok := range trendWindows {
			if d == ok {
				days = d
			}
		}
	}
	v := trendView{common: s.commonFor(r, "Tendance de la ligne"), Days: days,
		WeatherOn: st.Values.Bool(settings.WeatherOn)}
	for _, d := range trendWindows {
		label := strconv.Itoa(d) + " jours"
		if d == 365 {
			label = "1 an"
		}
		v.TrendWins = append(v.TrendWins, trendWindow{Days: d, Label: label, Active: d == days})
	}

	now := s.opts.Now().Local()
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).AddDate(0, 0, 1)
	start := end.AddDate(0, 0, -days)
	line, err := store.DailyLine(ctx, s.db, start, end)
	if err != nil {
		s.fail(w, err)
		return
	}
	weather, err := store.Weather(ctx, s.db, start.Format(time.DateOnly), end.AddDate(0, 0, -1).Format(time.DateOnly))
	if err != nil {
		s.fail(w, err)
		return
	}
	v.HasData, v.DayCount = len(line) > 0, len(line)
	v.HasWeather = len(weather) > 0

	// Une serie par mesure, un point par jour, a midi : un jour sans releve
	// interrompt la courbe.
	byDay := map[string]store.DayLine{}
	for _, d := range line {
		byDay[d.Day] = d
	}
	temp := map[string]*float64{}
	for _, d := range weather {
		temp[d.Day] = d.Mean
	}
	var ds, snr, us, tm []Sample
	var dsX, dsY, usX, usY, dsT, dsP, usT, usP []float64
	for i := range days {
		day := start.AddDate(0, 0, i)
		key := day.Format(time.DateOnly)
		at := day.Add(12 * time.Hour)
		d := byDay[key]
		ds = append(ds, Sample{at, d.DSPower})
		snr = append(snr, Sample{at, d.DSSNR})
		us = append(us, Sample{at, d.USPower})
		tm = append(tm, Sample{at, temp[key]})
		x := float64(i)
		if d.DSPower != nil {
			dsX, dsY = append(dsX, x), append(dsY, *d.DSPower)
			if t := temp[key]; t != nil {
				dsT, dsP = append(dsT, *t), append(dsP, *d.DSPower)
			}
		}
		if d.USPower != nil {
			usX, usY = append(usX, x), append(usY, *d.USPower)
			if t := temp[key]; t != nil {
				usT, usP = append(usT, *t), append(usP, *d.USPower)
			}
		}
	}

	if v.HasData {
		v.Findings = append(v.Findings,
			slopeFinding("Puissance descendante", "dB", dsX, dsY),
			slopeFinding("Émission", "dB", usX, usY))
		if len(weather) > 0 {
			v.Findings = append(v.Findings,
				weatherFinding("Puissance descendante et température", dsT, dsP),
				weatherFinding("Émission et température", usT, usP))
		}

		t := st.Profile.Thresholds
		dsSeries := Series{Label: "Puissance descendante moyenne", Points: ds, Unit: "dBmV"}
		usSeries := Series{Label: "Émission moyenne", Points: us, Unit: "dBmV"}
		snrSeries := Series{Label: "SNR moyen", Points: snr, Unit: "dB"}
		if st.Profile.Calibrated {
			if b, ok := t.DownstreamPowerFor(t.DownstreamDefault); ok {
				dsSeries.Band = &[2]float64{b.Good.Min, b.Good.Max}
			}
			if b, ok := t.UpstreamPowerFor(t.UpstreamDefault); ok {
				usSeries.Band = &[2]float64{b.Good.Min, b.Good.Max}
			}
			if b, ok := t.SNRFor(t.SNRDefault); ok {
				floor := b.GoodMin
				snrSeries.Floor = &floor
			}
		}
		v.Charts = append(v.Charts,
			trendChart{"Puissance descendante", "Moyenne journalière, dBmV.", template.HTML(SVG(dsSeries))},
			trendChart{"SNR", "Moyenne journalière, dB.", template.HTML(SVG(snrSeries))},
			trendChart{"Émission", "Moyenne journalière, dBmV.", template.HTML(SVG(usSeries))})
	}
	if len(weather) > 0 {
		v.Charts = append(v.Charts, trendChart{"Température extérieure",
			"Moyenne journalière, °C.", template.HTML(SVG(Series{Label: "Température moyenne", Points: tm, Unit: "°C"}))})
	}
	switch {
	case v.WeatherOn && st.Weather != nil:
		if p, last, err := st.Weather.Status(); err != nil {
			v.WeatherNote = "Météo indisponible : " + err.Error() + "."
		} else if !last.IsZero() {
			v.WeatherNote = "Température relevée pour " + p.Name + ", mise à jour " + Ago(last, s.opts.Now()) + "."
		}
	case v.WeatherOn && st.WeatherErr != nil:
		v.WeatherNote = "Météo indisponible : " + st.WeatherErr.Error() + "."
	}
	s.render(w, http.StatusOK, "tendance", v)
}

// fit ajuste y = a + b·x par les moindres carres et renvoie aussi le
// coefficient de correlation de Pearson.
func fit(xs, ys []float64) (b, r float64, ok bool) {
	n := float64(len(xs))
	if len(xs) < minDays || len(xs) != len(ys) {
		return 0, 0, false
	}
	var sx, sy, sxx, syy, sxy float64
	for i := range xs {
		sx += xs[i]
		sy += ys[i]
		sxx += xs[i] * xs[i]
		syy += ys[i] * ys[i]
		sxy += xs[i] * ys[i]
	}
	vx, vy := n*sxx-sx*sx, n*syy-sy*sy
	if vx <= 0 {
		return 0, 0, false
	}
	b = (n*sxy - sx*sy) / vx
	if vy > 0 {
		r = (n*sxy - sx*sy) / math.Sqrt(vx*vy)
	}
	return b, r, true
}

// slopeFinding resume la pente d'une mesure, en dB par mois.
func slopeFinding(title, unit string, xs, ys []float64) trendFinding {
	b, _, ok := fit(xs, ys)
	if !ok {
		return trendFinding{Title: title, Text: "Disponible après " +
			strconv.Itoa(minDays) + " jours."}
	}
	month := b * 30
	switch {
	case math.Abs(month) < stableSlope:
		return trendFinding{Title: title, Text: "Stable : moins de " + dec(stableSlope) + nbsp + unit + " de variation par mois."}
	case month < 0:
		return trendFinding{Title: title, Text: "En baisse d'environ " + dec(-month) + nbsp + unit + " par mois.", Alert: month < -1}
	default:
		return trendFinding{Title: title, Text: "En hausse d'environ " + dec(month) + nbsp + unit + " par mois.", Alert: month > 1}
	}
}

// weatherFinding dit si une mesure suit la temperature.
func weatherFinding(title string, temps, values []float64) trendFinding {
	b, r, ok := fit(temps, values)
	if !ok {
		return trendFinding{Title: title, Text: "Disponible après " +
			strconv.Itoa(minDays) + " jours."}
	}
	if math.Abs(r) < linked {
		return trendFinding{Title: title, Text: "Pas de lien net avec la température sur la période."}
	}
	sense := "plus forte quand il fait chaud"
	if b < 0 {
		sense = "plus forte quand il fait froid"
	}
	return trendFinding{Title: title, Text: "Elle suit la température : environ " + dec(math.Abs(b*10)) + nbsp +
		"dB d'écart pour 10" + nbsp + "°C, " + sense + "."}
}
