// Package collect porte la boucle de releve.
package collect

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/store"
)

// Source fournit un releve brut. Le pilote du modem l'implemente.
type Source interface {
	Snapshot(ctx context.Context) (docsis.RawSnapshot, error)
}

type Options struct {
	Interval  time.Duration
	Retention store.Retention
	// PurgeEvery espace les purges : les appliquer a chaque releve couterait
	// une lecture de taille de base toutes les dix minutes pour rien.
	PurgeEvery time.Duration
	Logger     *slog.Logger
	// Observer est prevenu de chaque releve, reussi ou manque ; facultatif.
	Observer Observer
	// Pace donne le delai avant le releve suivant ; Interval sinon. Il
	// permet de rapprocher les releves pendant un incident.
	Pace func(ctx context.Context) time.Duration
	// Wake fait recalculer l'attente en cours, par exemple apres une
	// demande de redemarrage ; facultatif.
	Wake <-chan struct{}
	// Now est injectable pour les tests.
	Now func() time.Time
}

func (o *Options) normalize() {
	if o.Interval <= 0 {
		o.Interval = 10 * time.Minute
	}
	if o.PurgeEvery <= 0 {
		o.PurgeEvery = 24 * time.Hour
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
}

// Observer suit la collecte : le journal des evenements l'implemente.
type Observer interface {
	// Recorded est appele apres l'enregistrement d'un releve.
	Recorded(ctx context.Context, snap docsis.Snapshot)
	// Failed est appele quand le modem n'a pas fourni de releve.
	Failed(ctx context.Context, err error)
}

// Observers relaie la collecte a plusieurs observateurs, dans l'ordre.
type Observers []Observer

func (o Observers) Recorded(ctx context.Context, snap docsis.Snapshot) {
	for _, ob := range o {
		ob.Recorded(ctx, snap)
	}
}

func (o Observers) Failed(ctx context.Context, err error) {
	for _, ob := range o {
		ob.Failed(ctx, err)
	}
}

// Collector interroge le modem et ecrit ce qu'il lit.
type Collector struct {
	source Source
	db     *sql.DB
	opts   Options
}

func New(source Source, db *sql.DB, opts Options) *Collector {
	opts.normalize()
	return &Collector{source: source, db: db, opts: opts}
}

// Run boucle jusqu'a l'annulation du contexte. Le premier releve est
// immediat ; le delai suivant court a la fin de chaque releve, sans
// chevauchement.
func (c *Collector) Run(ctx context.Context) error {
	purgeTicker := time.NewTicker(c.opts.PurgeEvery)
	defer purgeTicker.Stop()

	c.collectOnce(ctx)
	// Une purge des le depart : le minuteur repart de zero a chaque relance
	// (reglage enregistre, mise a jour), et une instance relancee plus d'une
	// fois par jour ne purgerait jamais.
	c.purge(ctx)
	timer := time.NewTimer(c.delay(ctx))
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			c.collectOnce(ctx)
			timer.Reset(c.delay(ctx))
		case <-purgeTicker.C:
			c.purge(ctx)
		case <-c.opts.Wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(c.delay(ctx))
		}
	}
}

func (c *Collector) delay(ctx context.Context) time.Duration {
	if c.opts.Pace != nil {
		if d := c.opts.Pace(ctx); d > 0 {
			return d
		}
	}
	return c.opts.Interval
}

// collectOnce prend un releve et l'enregistre. Un echec est journalise sans
// interrompre la boucle.
func (c *Collector) collectOnce(ctx context.Context) {
	// Le releve ne doit pas deborder sur le suivant.
	pollCtx, cancel := context.WithTimeout(ctx, c.opts.Interval)
	defer cancel()

	raw, err := c.source.Snapshot(pollCtx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		c.opts.Logger.Warn("relevé échoué", "erreur", err)
		if c.opts.Observer != nil {
			c.opts.Observer.Failed(context.WithoutCancel(ctx), err)
		}
		return
	}
	snap := raw.Normalize(c.opts.Now().UTC())
	// Un releve obtenu est enregistre meme si l'arret est demande entre-temps :
	// le perdre au moment d'un redemarrage du service effacerait justement la
	// mesure prise juste avant.
	saveCtx, cancelSave := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancelSave()
	if _, err := store.SaveSnapshot(saveCtx, c.db, snap); err != nil {
		c.opts.Logger.Error("enregistrement échoué", "erreur", err)
		return
	}
	if c.opts.Observer != nil {
		c.opts.Observer.Recorded(saveCtx, snap)
	}
	c.opts.Logger.Debug("relevé enregistré",
		"descendants", len(snap.Downstream), "montants", len(snap.Upstream))
}

func (c *Collector) purge(ctx context.Context) {
	purgeOnce(ctx, c.db, c.opts.Retention, c.opts.Now(), c.opts.Logger)
}

func purgeOnce(ctx context.Context, db *sql.DB, r store.Retention, now time.Time, logger *slog.Logger) {
	res, err := store.Purge(ctx, db, r, now)
	if err != nil {
		if ctx.Err() == nil {
			logger.Error("purge échouée", "erreur", err)
		}
		return
	}
	if res.ByAge > 0 || res.BySize > 0 {
		logger.Info("purge", "par_age", res.ByAge, "par_taille", res.BySize)
	}
}

// PurgeLoop applique la conservation sans collecte : modem non pris en
// charge ou pas encore configure, la sonde et le journal continuent de
// s'ecrire. Une purge tout de suite, puis toutes les every.
func PurgeLoop(ctx context.Context, db *sql.DB, r store.Retention, every time.Duration, logger *slog.Logger) error {
	o := Options{Retention: r, PurgeEvery: every, Logger: logger}
	o.normalize()
	ticker := time.NewTicker(o.PurgeEvery)
	defer ticker.Stop()
	for {
		purgeOnce(ctx, db, o.Retention, o.Now(), o.Logger)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// CollectOnce prend un unique releve, l'enregistre et le renvoie.
// Sert au diagnostic d'installation et aux tests.
func (c *Collector) CollectOnce(ctx context.Context) (docsis.Snapshot, error) {
	raw, err := c.source.Snapshot(ctx)
	if err != nil {
		return docsis.Snapshot{}, err
	}
	snap := raw.Normalize(c.opts.Now().UTC())
	if _, err := store.SaveSnapshot(ctx, c.db, snap); err != nil {
		return snap, err
	}
	return snap, nil
}
