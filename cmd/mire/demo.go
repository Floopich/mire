package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/demo"
	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/events"
	"github.com/floopich/mire/internal/modem"
	"github.com/floopich/mire/internal/service"
	"github.com/floopich/mire/internal/settings"
	"github.com/floopich/mire/internal/store"
	"github.com/floopich/mire/internal/webview"
)

// demoInstance est une demonstration complete : base jetable, service et
// interface, sur une ligne fictive. Elle ne touche jamais les vraies
// donnees.
type demoInstance struct {
	dir     string
	db      *sql.DB
	mgr     *service.Manager
	handler http.Handler
	logger  *slog.Logger
}

// startDemo prepare une demonstration. exit ajoute au bandeau le bouton qui
// la quitte, quand elle tourne a cote d'une instance a installer.
func startDemo(ctx context.Context, logger *slog.Logger, exit bool) (d *demoInstance, err error) {
	dir, err := os.MkdirTemp("", "mire-demo-")
	if err != nil {
		return nil, err
	}
	d = &demoInstance{dir: dir, logger: logger}
	defer func() {
		if err != nil {
			d.Close()
		}
	}()
	if d.db, err = store.Open(filepath.Join(dir, "mire.db")); err != nil {
		return nil, err
	}
	line := demo.New(time.Now())
	modem.Register(line.Model())
	forced := settings.Source{settings.ModemModel: demo.ModelID, settings.ModemPassword: demo.Password}
	// Un abonne VOO, modifiable dans les reglages pour voir la ligne jugee
	// par un autre profil.
	if err := settings.Set(ctx, d.db, forced, map[string]string{settings.Profile: "voo"}); err != nil {
		return nil, err
	}
	if err := fillDemo(ctx, d.db, forced, line, logger); err != nil {
		return nil, err
	}
	d.mgr = service.New(d.db, forced, logger)
	d.mgr.Version = currentVersion()
	if err := d.mgr.Start(ctx); err != nil {
		return nil, err
	}
	srv, err := webview.NewServer(d.db, webview.Options{DataDir: dir, Version: currentVersion(), Service: d.mgr,
		Logger: logger, Demo: true, DemoExit: exit})
	if err != nil {
		return nil, err
	}
	d.handler = srv.Routes()
	return d, nil
}

// Close arrete la demonstration et efface sa base.
func (d *demoInstance) Close() {
	if d.mgr != nil {
		d.mgr.Stop()
	}
	if d.db != nil {
		_ = d.db.Close()
	}
	_ = os.RemoveAll(d.dir)
	d.logger.Info("démonstration arrêtée")
}

// fillDemo ecrit l'historique de la ligne fictive, par les chemins de la
// collecte.
func fillDemo(ctx context.Context, db *sql.DB, forced settings.Source, line *demo.Line, logger *slog.Logger) error {
	start := time.Now()
	logger.Info("démonstration : écriture de l'historique fictif, quelques secondes")
	st, err := service.Resolve(ctx, db, forced)
	if err != nil {
		return err
	}
	rec := &events.Recorder{DB: db, Profile: st.Profile, Logger: logger,
		Analyze: func(ctx context.Context, snap docsis.Snapshot) (analyze.Health, error) {
			return st.Analyze(ctx, db, snap)
		}}
	if err := line.Fill(ctx, db, rec); err != nil {
		return fmt.Errorf("démonstration : %w", err)
	}
	logger.Info("démonstration prête : ligne et mesures fictives, rien n'est conservé",
		"historique_jours", demo.Days, "duree", time.Since(start).Round(time.Millisecond).String())
	return nil
}
