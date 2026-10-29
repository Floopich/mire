package webview

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"net/url"
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

// Le rapport rassemble, pour une periode, ce qu'un technicien demande : l'etat
// de la ligne dans le temps, les coupures, les canaux hors plage et le
// journal. Il s'imprime tel quel, ou s'enregistre en PDF depuis le
// navigateur. Une date d'intervention ajoute la comparaison avant/apres.

const (
	defaultReportDays = 30
	maxReportDays     = 366
)

var compareChoices = []int{7, 14, 30}

// periodReport resume une periode.
type periodReport struct {
	From, To time.Time // To exclu
	Line     store.PeriodLine
	Shares   map[analyze.Level]time.Duration
	Covered  time.Duration
	// Outages liste toutes les coupures ; les compteurs ecartent celles du
	// reseau local, qui ne relevent pas de l'operateur.
	Outages   []store.Outage
	Cuts      int
	Down      time.Duration
	Longest   store.Outage
	LongestD  time.Duration
	Local     int
	LocalDown time.Duration
	Probed    bool
	Restarts  int
	ModemDown int
	Lost      int
	Corrected int64
	Uncorr    int64
	// Debit mesure hors de Mire : medianes et plus bas descendant.
	SpeedCount         int
	SpeedDown, SpeedUp float64
	SpeedLow           float64
}

func (p periodReport) days() float64 { return p.To.Sub(p.From).Hours() / 24 }

// goodShare est la part du temps en bon etat, en %.
func (p periodReport) goodShare() (float64, bool) {
	if p.Covered <= 0 {
		return 0, false
	}
	return float64(p.Shares[analyze.Good]) / float64(p.Covered) * 100, true
}

// buildPeriod lit une periode. L'etat dans le temps se deduit des
// changements d'etat du journal : avant le premier, la ligne etait bonne.
func (s *Server) buildPeriod(ctx context.Context, from, to time.Time) (periodReport, error) {
	now := s.opts.Now()
	p := periodReport{From: from, To: to, Shares: map[analyze.Level]time.Duration{}}
	var err error
	if p.Line, err = store.LineOver(ctx, s.db, from, to); err != nil {
		return p, err
	}

	start, end := from, to
	if end.After(now) {
		end = now
	}
	first, err := store.FirstReadingTime(ctx, s.db)
	if err != nil {
		return p, err
	}
	if !first.IsZero() && start.Before(first) {
		start = first
	}
	if p.Line.Readings > 0 && end.After(start) {
		level := analyze.Good
		if e, ok, err := store.LastEventBefore(ctx, s.db, events.KindState, start); err != nil {
			return p, err
		} else if ok {
			level = analyze.Level(e.Level)
		}
		states, err := store.EventsBetween(ctx, s.db, start, end, events.KindState)
		if err != nil {
			return p, err
		}
		cursor := start
		for _, e := range states {
			p.Shares[level] += e.Time.Sub(cursor)
			level, cursor = analyze.Level(e.Level), e.Time
		}
		p.Shares[level] += end.Sub(cursor)
		p.Covered = end.Sub(start)
	}

	outages, err := store.Outages(ctx, s.db, from, to)
	if err != nil {
		return p, err
	}
	// Du plus ancien au plus recent, comme le reste du rapport.
	for i := len(outages) - 1; i >= 0; i-- {
		o := outages[i]
		p.Outages = append(p.Outages, o)
		a, b := o.Start, o.End
		if o.Ongoing() {
			b = now
		}
		if a.Before(from) {
			a = from
		}
		if b.After(to) {
			b = to
		}
		var part time.Duration
		if b.After(a) {
			part = b.Sub(a)
		}
		if o.Kind == probe.KindLocal {
			p.Local++
			p.LocalDown += part
			continue
		}
		p.Cuts++
		p.Down += part
		if d := o.Duration(now); d > p.LongestD {
			p.Longest, p.LongestD = o, d
		}
	}
	samples, err := store.ProbeSamples(ctx, s.db, from, to)
	if err != nil {
		return p, err
	}
	p.Probed = len(samples) > 0

	evs, err := store.EventsBetween(ctx, s.db, from, to, events.KindRestart, events.KindModemDown, events.KindChannelsLost)
	if err != nil {
		return p, err
	}
	for _, e := range evs {
		switch e.Kind {
		case events.KindRestart:
			p.Restarts++
		case events.KindModemDown:
			p.ModemDown++
		case events.KindChannelsLost:
			p.Lost++
		}
	}
	totals, err := store.ErrorTotals(ctx, s.db, from, to)
	if err != nil {
		return p, err
	}
	for _, e := range totals {
		p.Corrected += e.Corrected
		p.Uncorr += e.Uncorrected
	}
	speed, err := store.SpeedTests(ctx, s.db, from, to)
	if err != nil {
		return p, err
	}
	p.SpeedCount = len(speed)
	p.SpeedDown, p.SpeedUp, p.SpeedLow, _ = speedStats(speed)
	return p, nil
}

type shareRow struct {
	Level    analyze.Level
	Percent  string
	Duration string
}

type statRow struct {
	ID         int
	Modulation string
	Frequency  string
	Power      string
	PowerAvg   string
	SNR        string
	Errors     string
	Out        string
	Level      analyze.Level
}

type reportEvent struct {
	Time    time.Time
	Level   analyze.Level
	Note    bool
	Message string
}

type compareRow struct {
	Label, Before, After, Verdict string
	Tone                          string // ok, alerte ou vide
}

type comparison struct {
	Before, After string
	Short         string
	Rows          []compareRow
}

type reportView struct {
	common
	Du, Au, Intervention string
	Jours                int
	Choices              []int
	Err                  string
	Generated            time.Time
	PeriodLabel          string
	HasData              bool
	Summary              []string
	Shares               []shareRow
	DS, US               []statRow
	Outages              []outageRow
	CompensationTotal    string
	Events               []reportEvent
	EventsTruncated      bool
	Compare              *comparison
	// Exports sont les tableaux de la periode a telecharger.
	Exports []exportLink
}

type exportLink struct {
	Label, URL string
}

const maxReportEvents = 300

// reportPeriod lit la periode demandee, [from, to) en jours entiers ; trente
// jours jusqu'a aujourd'hui par defaut. Une saisie fautive rend la periode
// par defaut et le message a afficher.
func reportPeriod(q url.Values, today time.Time) (from, to time.Time, msg string) {
	to = today.AddDate(0, 0, 1)
	from = to.AddDate(0, 0, -defaultReportDays)
	if d, ok := parseDay(q.Get("au")); ok {
		to = d.AddDate(0, 0, 1)
	} else if q.Get("au") != "" {
		msg = "Date de fin illisible."
	}
	if d, ok := parseDay(q.Get("du")); ok {
		from = d
	} else if q.Get("du") != "" {
		msg = "Date de début illisible."
	}
	switch {
	case msg != "":
	case !from.Before(to):
		msg = "La date de début doit précéder la date de fin."
	case to.Sub(from) > maxReportDays*24*time.Hour:
		msg = "La période dépasse un an : raccourcissez-la."
	}
	if msg != "" {
		to = today.AddDate(0, 0, 1)
		from = to.AddDate(0, 0, -defaultReportDays)
	}
	return from, to, msg
}

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st := s.opts.Service.State()
	now := s.opts.Now()
	today := midnight(now)
	q := r.URL.Query()
	v := reportView{common: s.commonFor(r, "Rapport de la ligne"), Generated: now, Jours: 14, Choices: compareChoices}

	from, to, msg := reportPeriod(q, today)
	v.Err = msg
	if n, err := strconv.Atoi(q.Get("jours")); err == nil {
		for _, c := range compareChoices {
			if n == c {
				v.Jours = n
			}
		}
	}
	v.Du, v.Au = from.Format(time.DateOnly), to.AddDate(0, 0, -1).Format(time.DateOnly)
	v.PeriodLabel = "du " + from.Format("02/01/2006") + " au " + to.AddDate(0, 0, -1).Format("02/01/2006")

	p, err := s.buildPeriod(ctx, from, to)
	if err != nil {
		s.fail(w, err)
		return
	}
	v.HasData = p.Line.Readings > 0

	t := st.Profile.Thresholds
	var dsBand, usBand *[2]float64
	if st.Profile.Calibrated {
		if b, ok := t.DownstreamPowerFor(t.DownstreamDefault); ok {
			dsBand = &[2]float64{b.Good.Min, b.Good.Max}
		}
		if b, ok := t.UpstreamPowerFor(t.UpstreamDefault); ok {
			usBand = &[2]float64{b.Good.Min, b.Good.Max}
		}
	}
	ds, err := store.DownstreamStats(ctx, s.db, from, to, dsBand)
	if err != nil {
		s.fail(w, err)
		return
	}
	us, err := store.UpstreamStats(ctx, s.db, from, to, usBand)
	if err != nil {
		s.fail(w, err)
		return
	}
	totals, err := store.ErrorTotals(ctx, s.db, from, to)
	if err != nil {
		s.fail(w, err)
		return
	}
	var dsOut, usOut []string
	for _, c := range ds {
		row := statRow{ID: c.ChannelID, Modulation: c.Modulation, Frequency: FmtMHz(c.Frequency),
			Power: span2(c.PowerMin, c.PowerMax, "dBmV"), PowerAvg: Fmt(c.PowerAvg, "dBmV"),
			SNR: span2(c.SNRMin, c.SNRAvg, "dB"), Level: analyze.Good}
		if e, ok := totals[c.ChannelID]; ok {
			row.Errors = FmtCount(int(e.Uncorrected))
		}
		if dsBand != nil {
			row.Out = outShare(c)
			if c.OutOfBand > 0 {
				row.Level = analyze.Warning
				dsOut = append(dsOut, strconv.Itoa(c.ChannelID)+" ("+row.Out+")")
			}
		} else {
			row.Level = analyze.Unknown
		}
		v.DS = append(v.DS, row)
	}
	for _, c := range us {
		row := statRow{ID: c.ChannelID, Modulation: c.Modulation, Frequency: FmtMHz(c.Frequency),
			Power: span2(c.PowerMin, c.PowerMax, "dBmV"), PowerAvg: Fmt(c.PowerAvg, "dBmV"), Level: analyze.Good}
		if usBand != nil {
			row.Out = outShare(c)
			if c.OutOfBand > 0 {
				row.Level = analyze.Warning
				usOut = append(usOut, strconv.Itoa(c.ChannelID)+" ("+row.Out+")")
			}
		} else {
			row.Level = analyze.Unknown
		}
		v.US = append(v.US, row)
	}

	for _, l := range []analyze.Level{analyze.Good, analyze.Warning, analyze.Critical} {
		if d := p.Shares[l]; d > 0 {
			v.Shares = append(v.Shares, shareRow{Level: l, Percent: percentOf(d, p.Covered), Duration: events.Duration(d)})
		}
	}

	// Coupures et compensation.
	compShown := st.Profile.Country == "BE"
	fee := st.Values.Float(settings.MonthlyFee)
	var comp float64
	for _, o := range p.Outages {
		row := toOutageRow(o, now)
		if compShown && o.Kind != probe.KindLocal {
			if c := compensation.Compute(o.Duration(now), fee); c.Eligible {
				row.Compensation = euros(c.Amount)
				comp += c.Amount
			}
		}
		v.Outages = append(v.Outages, row)
	}
	if comp > 0 {
		v.CompensationTotal = euros(comp)
	}

	// Journal : les alertes et les notes, sans les retours a la normale.
	evs, err := store.EventsBetween(ctx, s.db, from, to)
	if err != nil {
		s.fail(w, err)
		return
	}
	for _, e := range evs {
		note := e.Kind == store.NoteKind
		if !note && analyze.Level(e.Level) < analyze.Warning {
			continue
		}
		if len(v.Events) == maxReportEvents {
			v.EventsTruncated = true
			break
		}
		v.Events = append(v.Events, reportEvent{Time: e.Time, Level: analyze.Level(e.Level), Note: note, Message: e.Message})
	}

	v.Summary = reportSummary(p, v.PeriodLabel, dsOut, usOut, dsBand != nil, now)

	// Donnees brutes : seulement les tableaux qui ont des lignes.
	has := map[string]bool{"descendant": v.HasData, "montant": v.HasData, "internet": p.Probed,
		"coupures": len(p.Outages) > 0, "debit": p.SpeedCount > 0, "journal": len(evs) > 0}
	period := url.Values{"du": {v.Du}, "au": {v.Au}}.Encode()
	for _, e := range store.Exports() {
		if has[e.Name] {
			v.Exports = append(v.Exports, exportLink{Label: e.Label, URL: "/rapport/donnees/" + e.Name + ".csv?" + period})
		}
	}

	// Comparaison avant/apres.
	if raw := q.Get("intervention"); raw != "" {
		d, ok := parseDay(raw)
		switch {
		case !ok:
			v.Err = "Date d'intervention illisible."
		case !d.Before(today.AddDate(0, 0, 1)):
			v.Err = "La date d'intervention ne peut pas être dans le futur."
		default:
			v.Intervention = d.Format(time.DateOnly)
			if v.Compare, err = s.compare(ctx, d, v.Jours, today.AddDate(0, 0, 1)); err != nil {
				s.fail(w, err)
				return
			}
		}
	}
	s.render(w, http.StatusOK, "rapport", v)
}

// compare met cote a cote les n jours qui precedent l'intervention et ceux
// qui la suivent, le jour meme compris dans l'apres.
func (s *Server) compare(ctx context.Context, day time.Time, n int, limit time.Time) (*comparison, error) {
	before, err := s.buildPeriod(ctx, day.AddDate(0, 0, -n), day)
	if err != nil {
		return nil, err
	}
	afterEnd := day.AddDate(0, 0, n)
	if afterEnd.After(limit) {
		afterEnd = limit
	}
	after, err := s.buildPeriod(ctx, day, afterEnd)
	if err != nil {
		return nil, err
	}
	c := &comparison{
		Before: fmt.Sprintf("%d jours avant", n),
		After:  fmt.Sprintf("Depuis le %s", day.Format("02/01")),
	}
	if d := int(math.Round(after.days())); d < n {
		c.Short = fmt.Sprintf("L'après ne couvre que %s.", plural(d, "jour", "jours"))
	}
	add := func(label string, b, a *float64, unit string, better func(b, a float64) int, tol float64) {
		row := compareRow{Label: label, Before: Fmt(b, unit), After: Fmt(a, unit)}
		if b != nil && a != nil {
			row.Verdict, row.Tone = verdict3(better(*b, *a), tol, *b, *a)
		}
		c.Rows = append(c.Rows, row)
	}
	higher := func(b, a float64) int { return sign(a - b) }
	lower := func(b, a float64) int { return sign(b - a) }
	// Une puissance moyenne n'est ni meilleure ni pire en soi : des canaux
	// hors plage de part et d'autre s'y compensent. La part des releves hors
	// plage, plus bas, en juge.
	c.Rows = append(c.Rows, compareRow{Label: "Puissance descendante moyenne",
		Before: Fmt(before.Line.DSPowerAvg, "dBmV"), After: Fmt(after.Line.DSPowerAvg, "dBmV")})
	add("SNR moyen", before.Line.DSSNRAvg, after.Line.DSSNRAvg, "dB", higher, 0.5)
	add("SNR le plus bas", before.Line.DSSNRMin, after.Line.DSSNRMin, "dB", higher, 0.5)
	add("Émission moyenne", before.Line.USPowerAvg, after.Line.USPowerAvg, "dBmV", lower, 0.5)

	if out, ok := s.outOfBand(ctx, before, after); ok {
		c.Rows = append(c.Rows, out...)
	}

	perDay := func(p periodReport) *float64 {
		if p.Line.Readings == 0 || p.days() <= 0 {
			return nil
		}
		v := float64(p.Uncorr) / p.days()
		return &v
	}
	b, a := perDay(before), perDay(after)
	row := compareRow{Label: "Erreurs non corrigées par jour", Before: countPtr(b), After: countPtr(a)}
	if b != nil && a != nil && math.Abs(*a-*b) > math.Max(10, 0.2*math.Max(*a, *b)) {
		row.Verdict, row.Tone = verdict3(sign(*b-*a), 0, *b, *a)
	} else if b != nil && a != nil {
		row.Verdict = "Inchangé"
	}
	c.Rows = append(c.Rows, row)

	row = compareRow{Label: "Coupures d'Internet", Before: outageText(before), After: outageText(after)}
	if before.Probed && after.Probed {
		row.Verdict, row.Tone = verdict3(sign(float64(before.Down-after.Down)), float64(time.Minute), float64(before.Down), float64(after.Down))
	}
	c.Rows = append(c.Rows, row)

	if before.SpeedCount > 0 || after.SpeedCount > 0 {
		speed := func(p periodReport, v float64) *float64 {
			if p.SpeedCount == 0 {
				return nil
			}
			return &v
		}
		// Un ecart de moins de 5 % tient a la mesure elle-meme.
		tol := 0.05 * math.Max(before.SpeedDown, after.SpeedDown)
		add("Débit descendant médian", speed(before, before.SpeedDown), speed(after, after.SpeedDown), "Mbit/s", higher, tol)
		tol = 0.05 * math.Max(before.SpeedUp, after.SpeedUp)
		add("Débit montant médian", speed(before, before.SpeedUp), speed(after, after.SpeedUp), "Mbit/s", higher, tol)
	}

	gb, okb := before.goodShare()
	ga, oka := after.goodShare()
	row = compareRow{Label: "Temps en bon état"}
	if okb {
		row.Before = percent(gb, 0)
	} else {
		row.Before = "—"
	}
	if oka {
		row.After = percent(ga, 0)
	} else {
		row.After = "—"
	}
	if okb && oka {
		row.Verdict, row.Tone = verdict3(sign(ga-gb), 1, gb, ga)
	}
	c.Rows = append(c.Rows, row)
	return c, nil
}

// outOfBand compare la part des releves hors de la plage nominale de
// puissance, voie par voie ; ok est faux pour un profil non calibre.
func (s *Server) outOfBand(ctx context.Context, before, after periodReport) ([]compareRow, bool) {
	st := s.opts.Service.State()
	t := st.Profile.Thresholds
	if !st.Profile.Calibrated {
		return nil, false
	}
	share := func(stats func(context.Context, *sql.DB, time.Time, time.Time, *[2]float64) ([]store.ChannelStats, error),
		p periodReport, band *[2]float64) *float64 {
		cs, err := stats(ctx, s.db, p.From, p.To, band)
		if err != nil {
			return nil
		}
		var out, n int
		for _, c := range cs {
			out += c.OutOfBand
			n += c.Readings
		}
		if n == 0 {
			return nil
		}
		v := float64(out) / float64(n) * 100
		return &v
	}
	var rows []compareRow
	add := func(label string, stats func(context.Context, *sql.DB, time.Time, time.Time, *[2]float64) ([]store.ChannelStats, error), band *[2]float64) {
		b, a := share(stats, before, band), share(stats, after, band)
		row := compareRow{Label: label, Before: "—", After: "—"}
		if b != nil {
			row.Before = percent(*b, 0)
		}
		if a != nil {
			row.After = percent(*a, 0)
		}
		if b != nil && a != nil {
			row.Verdict, row.Tone = verdict3(sign(*b-*a), 1, *b, *a)
		}
		rows = append(rows, row)
	}
	if b, ok := t.DownstreamPowerFor(t.DownstreamDefault); ok {
		add("Relevés hors plage, voie descendante", store.DownstreamStats, &[2]float64{b.Good.Min, b.Good.Max})
	}
	if b, ok := t.UpstreamPowerFor(t.UpstreamDefault); ok {
		add("Relevés hors plage, voie montante", store.UpstreamStats, &[2]float64{b.Good.Min, b.Good.Max})
	}
	return rows, true
}

// verdict3 rend « Mieux », « Moins bien » ou « Inchangé » ; un ecart sous la
// tolerance est inchange.
func verdict3(better int, tol, b, a float64) (string, string) {
	if math.Abs(a-b) <= tol || better == 0 {
		return "Inchangé", ""
	}
	if better > 0 {
		return "Mieux", "ok"
	}
	return "Moins bien", "alerte"
}

func sign(v float64) int {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}

// reportSummary ecrit la synthese en tete du rapport, en phrases.
func reportSummary(p periodReport, period string, dsOut, usOut []string, judged bool, now time.Time) []string {
	if p.Line.Readings == 0 {
		return []string{"Aucun relevé " + period + "."}
	}
	var out []string
	if p.Covered > 0 && judged {
		var parts []string
		words := map[analyze.Level]string{analyze.Good: "en bon état", analyze.Warning: "à surveiller", analyze.Critical: "critique"}
		for _, l := range []analyze.Level{analyze.Good, analyze.Warning, analyze.Critical} {
			if d := p.Shares[l]; d > 0 {
				parts = append(parts, words[l]+" "+percentOf(d, p.Covered)+" du temps")
			}
		}
		out = append(out, capitalize(period)+", la ligne a été "+joinFr(parts)+".")
	}
	switch {
	case !p.Probed && len(p.Outages) == 0:
		out = append(out, "Sonde Internet inactive sur la période.")
	case p.Cuts == 0:
		out = append(out, "Aucune coupure d'Internet constatée par la sonde.")
	default:
		s := "Internet a été coupé " + plural(p.Cuts, "fois", "fois") + ", " + events.Duration(p.Down) + " au total"
		if p.Cuts > 1 {
			s += " ; la plus longue a duré " + events.Duration(p.LongestD) + ", le " + FmtTime(p.Longest.Start)
		}
		out = append(out, s+".")
	}
	if p.Local > 0 {
		out = append(out, "Le réseau local a été coupé "+plural(p.Local, "fois", "fois")+", "+events.Duration(p.LocalDown)+
			" au total.")
	}
	var modem []string
	if p.Restarts > 0 {
		modem = append(modem, "a redémarré "+plural(p.Restarts, "fois", "fois"))
	}
	if p.ModemDown > 0 {
		modem = append(modem, "a été injoignable "+plural(p.ModemDown, "fois", "fois"))
	}
	if p.Lost > 0 {
		modem = append(modem, "a perdu des canaux "+plural(p.Lost, "fois", "fois"))
	}
	if len(modem) > 0 {
		out = append(out, "Le modem "+joinFr(modem)+".")
	}
	if judged {
		switch {
		case len(dsOut) == 0 && len(usOut) == 0:
			out = append(out, "Tous les canaux sont restés dans la plage nominale de puissance.")
		default:
			if len(dsOut) > 0 {
				out = append(out, "Canaux descendants hors plage (part du temps) : "+strings.Join(dsOut, ", ")+".")
			}
			if len(usOut) > 0 {
				out = append(out, "Canaux montants hors plage (part du temps) : "+strings.Join(usOut, ", ")+".")
			}
		}
	}
	if days := p.days(); days > 0 && p.Uncorr > 0 {
		out = append(out, plural(int(p.Uncorr), "erreur non corrigée", "erreurs non corrigées")+" sur la période, "+
			FmtCount(int(math.Round(float64(p.Uncorr)/days)))+" par jour en moyenne.")
	} else if days > 0 {
		out = append(out, "Aucune erreur non corrigée sur la période.")
	}
	if p.SpeedCount > 0 {
		out = append(out, "Débit médian sur "+plural(p.SpeedCount, "mesure", "mesures")+" : "+
			mbit(p.SpeedDown)+" en descendant, "+mbit(p.SpeedUp)+" en montant ; "+mbit(p.SpeedLow)+" au plus bas en descendant.")
	}
	return out
}

func joinFr(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " et " + parts[len(parts)-1]
}

func midnight(t time.Time) time.Time {
	t = t.Local()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
}

func parseDay(s string) (time.Time, bool) {
	d, err := time.ParseInLocation(time.DateOnly, strings.TrimSpace(s), time.Local)
	return d, err == nil
}

func span2(lo, hi *float64, unit string) string {
	if lo == nil || hi == nil {
		return "—"
	}
	if dec(*lo) == dec(*hi) {
		return dec(*lo) + nbsp + unit
	}
	return dec(*lo) + " à " + dec(*hi) + nbsp + unit
}

func outShare(c store.ChannelStats) string {
	if c.Readings == 0 {
		return "—"
	}
	return percent(float64(c.OutOfBand)/float64(c.Readings)*100, 0)
}

func percentOf(d, total time.Duration) string {
	if total <= 0 {
		return "—"
	}
	v := float64(d) / float64(total) * 100
	switch {
	case v > 0 && v < 1:
		return "moins de 1\u202f%"
	case v > 99 && v < 100:
		// Arrondi a 100 %, il contredirait l'etat minoritaire affiche a cote.
		return "plus de 99\u202f%"
	}
	return percent(v, 0)
}

func countPtr(v *float64) string {
	if v == nil {
		return "—"
	}
	return FmtCount(int(math.Round(*v)))
}

func outageText(p periodReport) string {
	switch {
	case len(p.Outages) == 0 && !p.Probed:
		return "non mesurées"
	case p.Cuts == 0:
		return "aucune"
	}
	return plural(p.Cuts, "coupure", "coupures") + ", " + events.Duration(p.Down)
}
