// Package probe verifie qu'Internet repond, par une connexion TCP vers
// quelques cibles publiques, fermee des qu'elle est etablie.
//
// Quand plus rien ne repond, la sonde interroge le modem : s'il repond, la
// ligne est coupee ; sinon, le reseau local.
package probe

import (
	"context"
	"database/sql"
	"log/slog"
	"net"
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

// Config regle la sonde.
type Config struct {
	// Targets sont les cibles hote:port interrogees a chaque tentative.
	Targets []string
	// Modem est l'adresse hote:port du modem, vide si elle est inconnue.
	Modem    string
	Interval time.Duration
	// Timeout borne chaque tentative ; 3 s par defaut.
	Timeout time.Duration
	// Threshold est le nombre de tentatives manquees de suite qui constate
	// une coupure ; 3 par defaut. Une tentative isolee peut se perdre sans
	// que la ligne soit coupee.
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
		c.Interval = 30 * time.Second
	}
	if c.Timeout <= 0 {
		c.Timeout = min(3*time.Second, c.Interval/2)
	}
	if c.Threshold <= 0 {
		c.Threshold = 3
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

// Prober fait tourner la sonde.
type Prober struct {
	db  *sql.DB
	cfg Config

	agg       aggregate
	failures  int
	firstFail time.Time
	open      *store.Outage
}

// New prepare une sonde.
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

// resume referme une coupure restee ouverte au dernier arret du service, a
// la derniere mesure enregistree : sa duree est donc minimale.
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

	if ok {
		p.failures = 0
		if p.open != nil {
			p.close(ctx, start)
		}
		return
	}
	p.failures++
	if p.failures == 1 {
		p.firstFail = start
	}
	if p.failures == p.cfg.Threshold && p.open == nil {
		p.openOutage(ctx)
	}
}

func (p *Prober) openOutage(ctx context.Context) {
	kind, level, msg := KindInternet, analyze.Critical, "Coupure d'Internet : aucune cible ne répond."
	if p.cfg.Modem != "" {
		if modemOK, _ := p.reach(ctx, []string{p.cfg.Modem}); modemOK {
			kind, msg = KindLine, "Coupure de la ligne : Internet ne répond plus, le modem si."
		} else {
			kind, level, msg = KindLocal, analyze.Warning,
				"Réseau local coupé : ni Internet ni le modem ne répondent."
		}
	}
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

// aggregate cumule les tentatives d'une tranche.
type aggregate struct {
	start          time.Time
	rounds, failed int
	sum, lo, hi    float64
	successes      int
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
	return s
}
