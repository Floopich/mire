package webview

import (
	"fmt"
	"html/template"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/compensation"
	"github.com/floopich/mire/internal/events"
	"github.com/floopich/mire/internal/probe"
	"github.com/floopich/mire/internal/settings"
	"github.com/floopich/mire/internal/store"
)

// outageRow est une coupure telle qu'affichee.
type outageRow struct {
	Start    time.Time
	Duration string
	Label    string
	// Cause explique ce que la sonde a constate.
	Cause   string
	Ongoing bool
	Level   analyze.Level
	// Compensation est la compensation legale estimee, vide si aucune.
	Compensation string
}

type internetView struct {
	common
	Probing      bool
	HasSamples   bool
	Availability string
	Latency      string
	Loss         string
	OutageCount  int
	OutageTotal  string
	Outages      []outageRow
	Charts       []template.HTML
	// Compensation legale belge : affichee pour un operateur belge.
	CompensationShown bool
	CompensationCount int
	CompensationTotal string
	// Profil horaire sur trente jours, independant de la periode choisie.
	ProfileDays    int
	ProfileVerdict string
	ProfileAlert   bool
	ProfileCharts  []template.HTML
	// Debit, mesure hors de Mire.
	Speed speedView
}

var outageLabels = map[string][2]string{
	probe.KindLine:     {"Ligne coupée", "Le modem répondait : coupure de la ligne."},
	probe.KindLocal:    {"Réseau local coupé", "Le modem ne répondait pas non plus."},
	probe.KindInternet: {"Internet injoignable", "Cause inconnue : adresse du modem non renseignée."},
}

func toOutageRow(o store.Outage, now time.Time) outageRow {
	l, known := outageLabels[o.Kind]
	if !known {
		l = [2]string{"Coupure", o.Detail}
	}
	row := outageRow{Start: o.Start, Duration: events.Duration(o.Duration(now)), Label: l[0], Cause: l[1],
		Ongoing: o.Ongoing(), Level: analyze.Critical}
	if o.Kind == probe.KindLocal {
		row.Level = analyze.Warning
	}
	return row
}

func (s *Server) handleInternet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	v := internetView{common: s.commonFor(r, "Internet"), Probing: s.opts.Service.State().Probing}
	now := s.opts.Now()
	from := now.Add(-time.Duration(v.Hours) * time.Hour)

	samples, err := store.ProbeSamples(ctx, s.db, from, now)
	if err != nil {
		s.fail(w, err)
		return
	}
	outages, err := store.Outages(ctx, s.db, from, now)
	if err != nil {
		s.fail(w, err)
		return
	}
	v.HasSamples = len(samples) > 0
	v.OutageCount = len(outages)
	var down time.Duration
	st := s.opts.Service.State()
	v.CompensationShown = st.Profile.Country == "BE"
	fee := st.Values.Float(settings.MonthlyFee)
	var compTotal float64
	for _, o := range outages {
		row := toOutageRow(o, now)
		// Une coupure du reseau local releve de l'installation de l'abonne :
		// elle n'ouvre aucun droit.
		if v.CompensationShown && o.Kind != probe.KindLocal {
			if c := compensation.Compute(o.Duration(now), fee); c.Eligible {
				row.Compensation = euros(c.Amount)
				compTotal += c.Amount
				v.CompensationCount++
			}
		}
		v.Outages = append(v.Outages, row)
		// Seule la part de la coupure comprise dans la fenetre compte.
		start, end := o.Start, o.End
		if o.Ongoing() {
			end = now
		}
		if start.Before(from) {
			start = from
		}
		if end.After(start) {
			down += end.Sub(start)
		}
	}
	if down > 0 {
		v.OutageTotal = events.Duration(down)
	}
	if v.CompensationCount > 0 {
		v.CompensationTotal = euros(compTotal)
	}

	if v.HasSamples {
		// La disponibilite se compte sur le temps sonde : avant la premiere
		// mesure, Mire ne sait rien de la connexion.
		start := samples[0].Start
		if start.Before(from) {
			start = from
		}
		if covered := now.Sub(start); covered > 0 {
			v.Availability = percent(100*(1-float64(down)/float64(covered)), 2)
		}
		var rounds, failed, ok int
		var sum float64
		latency := make([]Sample, 0, len(samples))
		const bucket = 5 * time.Minute
		for i, sm := range samples {
			// Un trou entre deux tranches, sonde arretee, interrompt la courbe.
			if i > 0 && sm.Start.Sub(samples[i-1].Start) > 2*bucket {
				latency = append(latency, Sample{Time: samples[i-1].Start.Add(bucket)})
			}
			latency = append(latency, Sample{Time: sm.Start.Add(bucket / 2), Value: sm.RTTAvg})
			rounds += sm.Rounds
			failed += sm.Failed
			if sm.RTTAvg != nil {
				n := sm.Rounds - sm.Failed
				sum += *sm.RTTAvg * float64(n)
				ok += n
			}
		}
		if ok > 0 {
			v.Latency = strconv.Itoa(int(math.Round(sum/float64(ok)))) + nbsp + "ms"
		}
		v.Loss = percent(100*float64(failed)/float64(max(rounds, 1)), 1)
		v.Charts = append(v.Charts,
			template.HTML(SVG(Series{Label: "Latence", Points: latency, Unit: "ms", ZeroBased: true})),
			template.HTML(LossBars("Tentatives sans réponse", samples, from, now)))
	}
	if v.Speed, err = s.speedFor(ctx, from, now); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.addProfile(r, &v, now); err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, http.StatusOK, "internet", v)
}

// addProfile ajoute le profil horaire des trente derniers jours.
func (s *Server) addProfile(r *http.Request, v *internetView, now time.Time) error {
	samples, err := store.ProbeSamples(r.Context(), s.db, now.AddDate(0, 0, -profileDays), now)
	if err != nil {
		return err
	}
	p := hourlyProfile(samples, now.Location())
	for _, h := range p {
		v.ProfileDays = max(v.ProfileDays, h.Days)
	}
	verdict, alert := profileVerdict(p)
	if verdict == "" {
		return nil
	}
	v.ProfileVerdict, v.ProfileAlert = verdict, alert
	usual, _ := usualLatency(p)
	v.ProfileCharts = []template.HTML{
		template.HTML(HourBars("Latence moyenne par heure", p,
			func(h hourStat) (float64, bool) {
				if h.Latency == nil {
					return 0, false
				}
				return *h.Latency, true
			},
			func(h int, val float64) string { return fmt.Sprintf("%d h : %s en moyenne", h, ms(val)) },
			func(val float64) string { return ms(val) }, "Aucune réponse mesurée.",
			func(h int) bool { return busyHour(p[h], usual) }, "bar-heure")),
		template.HTML(HourBars("Tentatives sans réponse par heure", p,
			func(h hourStat) (float64, bool) { return h.Loss, h.Rounds > 0 },
			func(h int, val float64) string { return fmt.Sprintf("%d h : %s sans réponse", h, percent(val, 2)) },
			lossTick, "Aucune tentative sans réponse.", nil, "bar-noncorr")),
	}
	return nil
}

// euros rend un montant a la francaise : « 2,50 € ».
func euros(v float64) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', 2, 64), ".", ",", 1) + nbsp + "€"
}

// percent rend un pourcentage a la francaise : « 99,95 % ».
func percent(v float64, decimals int) string {
	s := strconv.FormatFloat(v, 'f', decimals, 64)
	return strings.Replace(s, ".", ",", 1) + " %"
}
