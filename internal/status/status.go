// Package status rassemble l'etat present de la ligne : le dernier releve et
// son verdict, la coupure en cours, la derniere mesure de la sonde.
//
// Les metriques et Home Assistant publient la meme chose que la vue
// d'ensemble ; ce paquet en est la source unique.
package status

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/events"
	"github.com/floopich/mire/internal/probe"
	"github.com/floopich/mire/internal/store"
)

// Analyzer qualifie un releve avec les reglages en vigueur.
type Analyzer func(context.Context, docsis.Snapshot) (analyze.Health, error)

// Status est l'etat present de la ligne.
type Status struct {
	// HasData est faux tant qu'aucun releve n'existe.
	HasData  bool
	Snapshot docsis.Snapshot
	Health   analyze.Health

	// Outage dit si une coupure d'Internet est en cours, depuis OutageSince.
	Outage      bool
	OutageSince time.Time

	// Probe est la derniere tranche de la sonde, si elle a deja mesure.
	Probe    store.ProbeSample
	HasProbe bool

	// Fresh dit si le dernier releve est recent : le modem repond et la
	// collecte tourne. Il est faux sans releve.
	Fresh bool
	// LastOutage est la derniere coupure d'Internet, en cours ou finie ; les
	// coupures du reseau local, qui ne relevent pas de la ligne, en sont
	// ecartees.
	LastOutage    store.Outage
	HasLastOutage bool
	Outages24h    int
	Outages30d    int
	// LastRestart est l'heure du dernier redemarrage du modem constate.
	LastRestart time.Time
	// Uncorrected24h compte les erreurs non corrigees des dernieres 24 h.
	Uncorrected24h int64
	// Now est l'heure de la lecture.
	Now time.Time
}

// Config regle la lecture.
type Config struct {
	Analyze Analyzer
	// Interval est le delai entre deux releves : un releve plus vieux que
	// StaleAfter(Interval) n'est plus frais.
	Interval time.Duration
	// Now est injectable pour les tests.
	Now func() time.Time
}

// StaleAfter est l'age au-dela duquel le dernier releve est perime : trois
// intervalles, 30 minutes au moins.
func StaleAfter(interval time.Duration) time.Duration {
	return max(3*interval, 30*time.Minute)
}

// Read lit l'etat present.
func Read(ctx context.Context, db *sql.DB, cfg Config) (Status, error) {
	now := time.Now()
	if cfg.Now != nil {
		now = cfg.Now()
	}
	analyzer := cfg.Analyze
	s := Status{Now: now}
	snap, err := store.LatestSnapshot(ctx, db)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return Status{}, err
	default:
		s.HasData, s.Snapshot = true, snap
		if s.Health, err = analyzer(ctx, snap); err != nil {
			return Status{}, err
		}
	}
	o, ok, err := store.CurrentOutage(ctx, db)
	if err != nil {
		return Status{}, err
	}
	s.Outage, s.OutageSince = ok, o.Start
	if s.Probe, s.HasProbe, err = store.LatestProbeSample(ctx, db); err != nil {
		return Status{}, err
	}
	s.Fresh = s.HasData && now.Sub(s.Snapshot.Timestamp) <= StaleAfter(cfg.Interval)

	outages, err := store.Outages(ctx, db, now.Add(-30*24*time.Hour), now)
	if err != nil {
		return Status{}, err
	}
	for _, o := range outages { // les plus recentes d'abord
		if o.Kind == probe.KindLocal {
			continue
		}
		if !s.HasLastOutage {
			s.LastOutage, s.HasLastOutage = o, true
		}
		s.Outages30d++
		if o.Ongoing() || o.End.After(now.Add(-24*time.Hour)) {
			s.Outages24h++
		}
	}
	if e, ok, err := store.LastEvent(ctx, db, events.KindRestart); err != nil {
		return Status{}, err
	} else if ok {
		s.LastRestart = e.Time
	}
	if s.HasData {
		totals, err := store.ErrorTotals(ctx, db, now.Add(-24*time.Hour), now)
		if err != nil {
			return Status{}, err
		}
		for _, e := range totals {
			s.Uncorrected24h += e.Uncorrected
		}
	}
	return s, nil
}

// Summary reprend les valeurs de la vue d'ensemble.
type Summary struct {
	DSPowerMin, DSPowerMax, DSSNRMin, USPowerMax *float64
	DSCount, USCount                             int
	// Attention compte les canaux a surveiller ou critiques.
	Attention int
}

// Summarize calcule le resume du dernier releve.
func (s Status) Summarize() Summary {
	var out Summary
	if !s.HasData {
		return out
	}
	out.DSCount, out.USCount = len(s.Snapshot.Downstream), len(s.Snapshot.Upstream)
	for _, c := range s.Snapshot.Downstream {
		out.DSPowerMin = lower(out.DSPowerMin, c.PowerDBmV)
		out.DSPowerMax = higher(out.DSPowerMax, c.PowerDBmV)
		out.DSSNRMin = lower(out.DSSNRMin, c.SNRDB)
	}
	for _, c := range s.Snapshot.Upstream {
		out.USPowerMax = higher(out.USPowerMax, c.PowerDBmV)
	}
	for _, list := range [][]analyze.ChannelHealth{s.Health.Downstream, s.Health.Upstream} {
		for _, c := range list {
			if c.Level >= analyze.Warning {
				out.Attention++
			}
		}
	}
	return out
}

// Loss rend la part de tentatives perdues de la derniere tranche, en %.
func (s Status) Loss() (float64, bool) {
	if !s.HasProbe || s.Probe.Rounds == 0 {
		return 0, false
	}
	return float64(s.Probe.Failed) / float64(s.Probe.Rounds) * 100, true
}

// Diagnosis decrit l'etat de la ligne en une phrase, comme le journal.
func (s Status) Diagnosis() string {
	if !s.HasData {
		return "Aucun relevé pour l'instant."
	}
	return events.Diagnosis(s.Health)
}

// LevelName rend un niveau en mot simple, pour les integrations.
func LevelName(l analyze.Level) string {
	switch l {
	case analyze.Good:
		return "bon"
	case analyze.Tolerated:
		return "tolere"
	case analyze.Warning:
		return "a_surveiller"
	case analyze.Critical:
		return "critique"
	default:
		return "inconnu"
	}
}

func lower(cur, v *float64) *float64 {
	if v == nil || (cur != nil && *cur <= *v) {
		return cur
	}
	x := *v
	return &x
}

func higher(cur, v *float64) *float64 {
	if v == nil || (cur != nil && *cur >= *v) {
		return cur
	}
	x := *v
	return &x
}
