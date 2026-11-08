// Package probe verifie qu'Internet repond, par une connexion TCP vers
// quelques cibles publiques, fermee des qu'elle est etablie.
//
// Quand plus rien ne repond, la sonde interroge le modem : s'il repond, la
// ligne est coupee ; sinon, le reseau local.
package probe

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/events"
	"github.com/floopich/mire/internal/store"
)

// Natures de coupure, telles qu'inscrites dans link_events.
const (
	KindLine     = "ligne"
	KindLocal    = "local"
	KindInternet = "internet"
)

const (
	// OutageAfter est la duree sans reponse qui fait une coupure ; en deca,
	// a partir de deux tentatives manquees de suite, c'est une micro-coupure.
	OutageAfter = 90 * time.Second
	// MicroBurst micro-coupures en une heure s'inscrivent au journal.
	MicroBurst = 5
	// microMaxInterval est l'intervalle au-dela duquel deux tentatives
	// manquees ne font plus une coupure breve.
	microMaxInterval = 30 * time.Second
)

type Config struct {
	// Targets sont les cibles hote:port interrogees a chaque tentative.
	Targets []string
	// Modem est l'adresse hote:port du modem, vide si elle est inconnue.
	Modem    string
	Interval time.Duration
	// Timeout borne chaque tentative ; 3 s par defaut.
	Timeout time.Duration
	// Threshold est le nombre de tentatives manquees de suite qui constate
	// une coupure : de quoi couvrir OutageAfter, et 3 au moins. Une tentative
	// isolee peut se perdre sans que la ligne soit coupee.
	Threshold int
	// Bucket est la tranche d'agregation des mesures ; 5 min par defaut.
	Bucket time.Duration
	Logger *slog.Logger
	// Dial et Now sont injectables pour les tests.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
	Now  func() time.Time
}

func (c *Config) normalize() {
	if c.Interval <= 0 {
		c.Interval = 5 * time.Second
	}
	if c.Timeout <= 0 {
		c.Timeout = min(3*time.Second, c.Interval/2)
	}
	if c.Threshold <= 0 {
		c.Threshold = max(3, int((OutageAfter+c.Interval-1)/c.Interval))
	}
	if c.Bucket <= 0 {
		c.Bucket = 5 * time.Minute
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	if c.Dial == nil {
		var d net.Dialer
		c.Dial = d.DialContext
	}
	if c.Now == nil {
		c.Now = time.Now
	}
}

type Prober struct {
	db  *sql.DB
	cfg Config

	agg aggregate
	// last est la latence de la derniere tentative reussie, zero apres un
	// echec : la gigue ne se mesure qu'entre deux reussites consecutives.
	last      time.Duration
	failures  int
	firstFail time.Time
	lastFail  time.Time
	// lastOK est la derniere tentative reussie : la borne haute d'une
	// micro-coupure.
	lastOK time.Time
	// microKind est la nature d'une micro-coupure en cours.
	microKind string
	open      *store.Outage
}

func New(db *sql.DB, cfg Config) *Prober {
	cfg.normalize()
	return &Prober{db: db, cfg: cfg}
}

// Run sonde jusqu'a l'annulation du contexte.
func (p *Prober) Run(ctx context.Context) error {
	p.resume(ctx)
	ticker := time.NewTicker(p.cfg.Interval)
	defer ticker.Stop()
	p.Round(ctx)
	for {
		select {
		case <-ctx.Done():
			p.flush(context.WithoutCancel(ctx))
			return ctx.Err()
		case <-ticker.C:
			p.Round(ctx)
		}
	}
}

// resumeWithin est l'ecart maximal entre la derniere mesure et la reprise
// pour qu'une coupure ouverte soit poursuivie plutot que refermee : la sonde
// vient d'etre relancee (reglage enregistre, mise a jour rapide), elle n'a
// rien manque d'important.
func (p *Prober) resumeWithin() time.Duration { return 2 * p.cfg.Bucket }

// resume reprend une coupure restee ouverte. Si la derniere mesure est
// recente, la sonde vient seulement d'etre relancee : la coupure continue et
// se refermera au premier retour d'Internet. Sinon, Mire a ete arrete : elle
// est refermee a la derniere mesure enregistree, sa duree est donc minimale.
func (p *Prober) resume(ctx context.Context) {
	o, ok, err := store.CurrentOutage(ctx, p.db)
	if err != nil || !ok {
		p.logErr(err)
		return
	}
	end, err := store.LastProbeSample(ctx, p.db)
	if err != nil {
		p.logErr(err)
		return
	}
	if !end.IsZero() && !end.Before(o.Start.Truncate(p.cfg.Bucket)) && p.cfg.Now().Sub(end) <= p.resumeWithin() {
		p.open = &o
		p.failures, p.firstFail = p.cfg.Threshold, o.Start
		p.cfg.Logger.Info("coupure en cours reprise", "nature", o.Kind)
		return
	}
	end = end.Add(p.cfg.Bucket)
	if end.Before(o.Start) || end.After(p.cfg.Now()) {
		end = o.Start
	}
	if err := store.CloseOutage(ctx, p.db, o.ID, end); err != nil {
		p.logErr(err)
		return
	}
	p.logErr(store.SaveEvents(ctx, p.db, []store.Event{{Time: end, Kind: events.KindOutageEnd, Level: int(analyze.Unknown),
		Message: "Fin de coupure inconnue (Mire arrêté) : au moins " +
			events.Duration(end.Sub(o.Start)) + "."}}))
}

// Round fait une tentative et en tire les consequences.
func (p *Prober) Round(ctx context.Context) {
	start := p.cfg.Now()
	ok, rtt := p.reach(ctx, p.cfg.Targets)
	if ctx.Err() != nil {
		return
	}
	bucket := start.UTC().Truncate(p.cfg.Bucket)
	if !p.agg.start.IsZero() && !p.agg.start.Equal(bucket) {
		p.flush(ctx)
	}
	p.agg.add(bucket, ok, rtt)
	p.jitter(ok, rtt)

	micro := p.cfg.Interval <= microMaxInterval
	if ok {
		switch {
		case p.open != nil:
			p.close(ctx, start)
		case micro && p.failures >= 2 && p.failures < p.cfg.Threshold:
			p.microCut(ctx, start)
		}
		p.failures, p.lastOK = 0, start
		return
	}
	p.failures++
	p.lastFail = start
	if p.failures == 1 {
		p.firstFail = start
	}
	if micro && p.failures == 2 && p.open == nil {
		p.microKind, _, _ = p.cause(ctx)
	}
	if p.failures == p.cfg.Threshold && p.open == nil {
		p.openOutage(ctx)
	}
}

// cause dit ou s'arrete la connexion : si le modem repond, la ligne ; sinon,
// le reseau local. Sans adresse de modem, Internet sans plus de precision.
func (p *Prober) cause(ctx context.Context) (kind string, level analyze.Level, msg string) {
	if p.cfg.Modem == "" {
		return KindInternet, analyze.Critical, "Coupure d'Internet : aucune cible ne répond."
	}
	if modemOK, _ := p.reach(ctx, []string{p.cfg.Modem}); modemOK {
		return KindLine, analyze.Critical, "Coupure de la ligne : Internet ne répond plus, le modem si."
	}
	return KindLocal, analyze.Warning, "Réseau local coupé : ni Internet ni le modem ne répondent."
}

// microCut inscrit une micro-coupure terminee a end. Sa duree n'est connue
// qu'a l'intervalle pres : au moins de la premiere a la derniere tentative
// manquee, au plus de la derniere reussite a la suivante.
func (p *Prober) microCut(ctx context.Context, end time.Time) {
	before := p.lastOK
	if before.IsZero() || before.After(p.firstFail) {
		before = p.firstFail.Add(-p.cfg.Interval)
	}
	m := store.MicroCut{Start: p.firstFail.UTC(), Min: p.lastFail.Sub(p.firstFail), Max: end.Sub(before), Kind: p.microKind}
	if err := store.SaveMicroCut(ctx, p.db, m); err != nil {
		p.logErr(err)
		return
	}
	p.cfg.Logger.Info("micro-coupure", "nature", m.Kind,
		"duree", fmt.Sprintf("%s à %s", m.Min.Round(time.Second), m.Max.Round(time.Second)))

	// Une rafale s'inscrit au journal, une fois par heure au plus.
	recent, err := store.MicroCuts(ctx, p.db, end.Add(-time.Hour), end.Add(time.Second))
	if err != nil || len(recent) < MicroBurst {
		p.logErr(err)
		return
	}
	last, seen, err := store.LastEvent(ctx, p.db, events.KindMicroBurst)
	if err != nil || (seen && end.Sub(last.Time) < time.Hour) {
		p.logErr(err)
		return
	}
	p.logErr(store.SaveEvents(ctx, p.db, []store.Event{{Time: end, Kind: events.KindMicroBurst,
		Level: int(analyze.Warning), Message: burstMessage(recent)}}))
}

// burstMessage resume une rafale : « Micro-coupures répétées : 5 en une
// heure, de la ligne. »
func burstMessage(cuts []store.MicroCut) string {
	counts := map[string]int{}
	for _, c := range cuts {
		counts[c.Kind]++
	}
	of := map[string]string{KindLine: "de la ligne", KindLocal: "du réseau local", KindInternet: "d'Internet"}
	short := map[string]string{KindLine: "ligne", KindLocal: "réseau local", KindInternet: "Internet"}
	msg := fmt.Sprintf("Micro-coupures répétées : %d en une heure", len(cuts))
	if len(counts) == 1 {
		return msg + ", " + of[cuts[0].Kind] + "."
	}
	kinds := make([]string, 0, len(counts))
	for k := range counts {
		kinds = append(kinds, k)
	}
	sort.Slice(kinds, func(i, j int) bool {
		return counts[kinds[i]] > counts[kinds[j]] || (counts[kinds[i]] == counts[kinds[j]] && kinds[i] < kinds[j])
	})
	parts := make([]string, len(kinds))
	for i, k := range kinds {
		parts[i] = fmt.Sprintf("%s : %d", short[k], counts[k])
	}
	return msg + " (" + strings.Join(parts, ", ") + ")."
}

func (p *Prober) openOutage(ctx context.Context) {
	kind, level, msg := p.cause(ctx)
	id, err := store.OpenOutage(ctx, p.db, kind, p.firstFail, msg)
	if err != nil {
		p.logErr(err)
		return
	}
	p.open = &store.Outage{ID: id, Start: p.firstFail.UTC(), Kind: kind, Detail: msg}
	p.cfg.Logger.Warn("coupure", "nature", kind)
	p.logErr(store.SaveEvents(ctx, p.db, []store.Event{{Time: p.firstFail, Kind: events.KindOutage, Level: int(level), Message: msg}}))
}

func (p *Prober) close(ctx context.Context, end time.Time) {
	o := *p.open
	p.open = nil
	if err := store.CloseOutage(ctx, p.db, o.ID, end); err != nil {
		p.logErr(err)
		return
	}
	what := map[string]string{KindLine: "de la ligne", KindLocal: "du réseau local", KindInternet: "d'Internet"}[o.Kind]
	p.cfg.Logger.Info("fin de coupure", "nature", o.Kind, "duree", end.Sub(o.Start).Round(time.Second).String())
	p.logErr(store.SaveEvents(ctx, p.db, []store.Event{{Time: end, Kind: events.KindOutageEnd, Level: int(analyze.Good),
		Message: "Fin de coupure " + what + ", après " + events.Duration(end.Sub(o.Start)) + "."}}))
}

// reach tente les cibles en parallele et renvoie la latence de la plus
// rapide a repondre.
func (p *Prober) reach(ctx context.Context, targets []string) (bool, time.Duration) {
	if len(targets) == 0 {
		return false, 0
	}
	ctx, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()
	type result struct {
		ok  bool
		rtt time.Duration
	}
	results := make(chan result, len(targets))
	for _, t := range targets {
		go func() {
			begin := time.Now()
			conn, err := p.cfg.Dial(ctx, "tcp", t)
			if err != nil {
				results <- result{}
				return
			}
			rtt := time.Since(begin)
			_ = conn.Close()
			results <- result{true, rtt}
		}()
	}
	for range targets {
		if r := <-results; r.ok {
			return true, r.rtt
		}
	}
	return false, 0
}

func (p *Prober) flush(ctx context.Context) {
	if p.agg.rounds == 0 {
		return
	}
	p.logErr(store.SaveProbeSample(ctx, p.db, p.agg.sample()))
	p.agg = aggregate{}
}

func (p *Prober) logErr(err error) {
	if err != nil {
		p.cfg.Logger.Error("sonde", "erreur", err)
	}
}

type aggregate struct {
	start          time.Time
	rounds, failed int
	sum, lo, hi    float64
	successes      int
	jitter         float64
	pairs          int
}

// jitter compte l'ecart de latence avec la tentative precedente, si les
// deux ont reussi.
func (p *Prober) jitter(ok bool, rtt time.Duration) {
	if !ok {
		p.last = 0
		return
	}
	if p.last > 0 {
		p.agg.pair(rtt - p.last)
	}
	p.last = rtt
}

// pair compte l'ecart de latence entre deux tentatives reussies consecutives.
func (a *aggregate) pair(d time.Duration) {
	if d < 0 {
		d = -d
	}
	a.jitter += float64(d.Microseconds()) / 1000
	a.pairs++
}

func (a *aggregate) add(bucket time.Time, ok bool, rtt time.Duration) {
	a.start = bucket
	a.rounds++
	if !ok {
		a.failed++
		return
	}
	ms := float64(rtt.Microseconds()) / 1000
	if a.successes == 0 || ms < a.lo {
		a.lo = ms
	}
	if ms > a.hi {
		a.hi = ms
	}
	a.sum += ms
	a.successes++
}

func (a aggregate) sample() store.ProbeSample {
	s := store.ProbeSample{Start: a.start, Rounds: a.rounds, Failed: a.failed}
	if a.successes > 0 {
		avg := a.sum / float64(a.successes)
		lo, hi := a.lo, a.hi
		s.RTTMin, s.RTTAvg, s.RTTMax = &lo, &avg, &hi
	}
	if a.pairs > 0 {
		j := a.jitter / float64(a.pairs)
		s.Jitter, s.JitterPairs = &j, a.pairs
	}
	return s
}
