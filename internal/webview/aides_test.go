package webview

import (
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/floopich/mire/internal/auth"
	"github.com/floopich/mire/internal/journal"
	"github.com/floopich/mire/internal/modem"
	"github.com/floopich/mire/internal/service"
	"github.com/floopich/mire/internal/settings"
)

// motDePasseTest est le mot de passe de l'interface dans les tests.
const motDePasseTest = "mot-de-passe-de-test"

// fauxService resout les reglages comme le vrai, sans lancer de collecte :
// les tests choisissent si elle tourne.
type fauxService struct {
	db         *sql.DB
	forced     settings.Source
	collecting bool
	interval   time.Duration
	st         service.State
	reloads    int
	// pilote remplace le pilote partage du modem.
	pilote modem.Driver
}

func (f *fauxService) State() service.State    { return f.st }
func (f *fauxService) Forced() settings.Source { return f.forced }

func (f *fauxService) Reload(ctx context.Context) error {
	st, err := service.Resolve(ctx, f.db, f.forced)
	if err != nil {
		return err
	}
	st.Collecting = f.collecting
	if f.pilote != nil {
		st.Modem = f.pilote
	}
	if f.interval > 0 {
		st.Interval = f.interval
	}
	f.st = st
	f.reloads++
	return nil
}

// essai decrit l'instance montee par un test.
type essai struct {
	Collecting bool
	Interval   time.Duration
	Now        func() time.Time
	Version    string
	Forced     settings.Source
	// SansMotDePasse laisse l'instance au premier demarrage.
	SansMotDePasse bool
	// DataDir est le dossier des donnees ; un dossier temporaire sinon.
	DataDir string
	// Pilote est le pilote partage du modem.
	Pilote modem.Driver
	// Demo ouvre l'interface du mode demonstration.
	Demo, DemoExit bool
	// StartDemo propose la demonstration a l'accueil.
	StartDemo func(context.Context) (http.Handler, func(), error)
	// Journal garde les lignes du service.
	Journal *journal.Journal
}

// monter construit l'interface sur db ; elle renvoie aussi le faux service
// pour observer les rechargements.
func monter(t *testing.T, db *sql.DB, e essai) (http.Handler, *fauxService) {
	t.Helper()
	if e.Forced == nil {
		e.Forced = settings.Source{}
	}
	svc := &fauxService{db: db, forced: e.Forced, collecting: e.Collecting, interval: e.Interval, pilote: e.Pilote}
	if err := svc.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc.reloads = 0
	if e.DataDir == "" {
		e.DataDir = t.TempDir()
	}
	s, err := NewServer(db, Options{DataDir: e.DataDir, Version: e.Version, Now: e.Now, Service: svc, Demo: e.Demo, DemoExit: e.DemoExit, StartDemo: e.StartDemo, Journal: e.Journal})
	if err != nil {
		t.Fatalf("serveur: %v", err)
	}
	if !e.SansMotDePasse {
		if err := auth.SetPassword(context.Background(), db, motDePasseTest); err != nil {
			t.Fatal(err)
		}
	}
	return s.Routes(), svc
}

// session ouvre une session sans passer par le formulaire.
func session(t *testing.T, db *sql.DB) string {
	t.Helper()
	token, err := auth.NewSessions(db).Create(context.Background())
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	return token
}

// serveur monte l'interface et ouvre une session : les tests de rendu portent
// sur les pages, l'authentification a les siens.
func serveur(t *testing.T, db *sql.DB) (http.Handler, string) {
	t.Helper()
	h, _ := monter(t, db, essai{Collecting: true})
	return h, session(t, db)
}

// serveurAvec monte l'interface sur une base de quelques releves.
func serveurAvec(t *testing.T, releves int, e essai) (http.Handler, string) {
	t.Helper()
	db := baseRemplie(t, releves)
	h, _ := monter(t, db, e)
	return h, session(t, db)
}
