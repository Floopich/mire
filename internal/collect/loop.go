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

// Options regle la boucle.
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
	if o.Retention == (store.Retention{}) {
		o.Retention = store.DefaultRetention
	}
}

// Observer suit la collecte : le journal des evenements l'implemente.
type Observer interface {
	// Recorded est appele apres l'enregistrement d'un releve.
	Recorded(ctx context.Context, snap docsis.Snapshot)
	// Failed est appele quand le modem n'a pas fourni de releve.
	Failed(ctx context.Context, err error)
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

// delay renvoie l'attente avant le prochain releve.
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
	res, err := store.Purge(ctx, c.db, c.opts.Retention, c.opts.Now())
	if err != nil {
		c.opts.Logger.Error("purge échouée", "erreur", err)
		return
	}
	if res.ByAge > 0 || res.BySize > 0 {
		c.opts.Logger.Info("purge", "par_age", res.ByAge, "par_taille", res.BySize)
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
