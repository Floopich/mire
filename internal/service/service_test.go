package service

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/settings"
	"github.com/floopich/mire/internal/store"
)

func base(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestUneInstanceNeuveSuitLeProfil(t *testing.T) {
	st, err := Resolve(context.Background(), base(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Profile.ID != "voo" || st.Model.ID != "cga4233" || st.ModelErr != nil {
		t.Fatalf("profil %s, modele %s (%v)", st.Profile.ID, st.Model.ID, st.ModelErr)
	}
	if st.Address != st.Profile.DefaultModemURL || st.User != st.Model.DefaultUser {
		t.Errorf("adresse %q, identifiant %q : attendu ceux du profil et du modele", st.Address, st.User)
	}
	if st.Interval != 10*time.Minute {
		t.Errorf("intervalle %v", st.Interval)
	}
	if _, err := st.Driver(); err == nil {
		t.Error("sans mot de passe du modem, aucun pilote ne doit etre construit")
	}
}

func TestLesReglagesEnregistresPrennentLeDessus(t *testing.T) {
	db := base(t)
	ctx := context.Background()
	if err := settings.Set(ctx, db, nil, map[string]string{
		settings.ModemURL: "http://192.168.0.1", settings.ModemUser: "technicien",
		settings.ModemPassword: "secret", settings.Interval: "30m",
		settings.RetentionDays: "90", settings.RetentionGB: "0,5",
	}); err != nil {
		t.Fatal(err)
	}
	st, err := Resolve(ctx, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Address != "http://192.168.0.1" || st.User != "technicien" || st.Interval != 30*time.Minute {
		t.Errorf("etat inattendu : %+v", st)
	}
	if r := st.Retention(); r.Days != 90 || r.MaxBytes != 1<<29 {
		t.Errorf("conservation %+v", r)
	}
	if _, err := st.Driver(); err != nil {
		t.Errorf("pilote : %v", err)
	}
}

func TestUnProfilDisparuRetombeSurLeGenerique(t *testing.T) {
	db := base(t)
	// Valeur ecrite directement : la page Reglages l'aurait refusee, mais le
	// profil a pu disparaitre d'une version a l'autre.
	if err := store.SetSetting(context.Background(), db, "reglage."+settings.Profile, "disparu"); err != nil {
		t.Fatal(err)
	}
	st, err := Resolve(context.Background(), db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Profile.ID != "generique" || st.Profile.Calibrated {
		t.Errorf("profil %s, attendu generique non calibre", st.Profile.ID)
	}
	if st.ModelErr == nil {
		t.Error("le profil generique ne designe aucun modem : il faut le dire")
	}
}

func TestLeRechargementDemarreEtArreteLaCollecte(t *testing.T) {
	db := base(t)
	ctx := context.Background()
	// La sonde est coupee : un test ne doit pas dependre d'Internet.
	m := New(db, settings.Source{settings.ModemURL: "http://127.0.0.1:9", settings.ProbeOn: "false"},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if m.State().Collecting {
		t.Fatal("sans mot de passe, la collecte ne doit pas tourner")
	}
	if err := settings.Set(ctx, db, m.Forced(), map[string]string{settings.ModemPassword: "secret"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if !m.State().Collecting {
		t.Fatal("la collecte devait demarrer apres le rechargement")
	}
	done := make(chan struct{})
	go func() { m.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("l'arret de la collecte ne rend pas la main")
	}
}

func TestLaSondeSuitSesReglages(t *testing.T) {
	db := base(t)
	ctx := context.Background()
	st, err := Resolve(ctx, db, settings.Source{settings.ModemURL: "https://192.168.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := st.ProbeConfig(nil)
	if len(cfg.Targets) != 3 || cfg.Modem != "192.168.0.1:443" || cfg.Interval != 30*time.Second {
		t.Errorf("configuration %+v", cfg)
	}
	if err := settings.Set(ctx, db, nil, map[string]string{settings.ProbeTargets: "10.0.0.1:53, [2606:4700::1111]:443", settings.ProbeInterval: "1m"}); err != nil {
		t.Fatal(err)
	}
	st, _ = Resolve(ctx, db, nil)
	cfg = st.ProbeConfig(nil)
	if len(cfg.Targets) != 2 || cfg.Targets[1] != "[2606:4700::1111]:443" || cfg.Interval != time.Minute || cfg.Modem != "192.168.100.1:80" {
		t.Errorf("configuration %+v", cfg)
	}
	if err := settings.Set(ctx, db, nil, map[string]string{settings.ProbeTargets: "1.1.1.1"}); err == nil {
		t.Error("une cible sans port doit etre refusee")
	}
}

func TestRelevesRapprochesApresUnIncident(t *testing.T) {
	db := base(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 26, 20, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	muet := slog.New(slog.NewTextHandler(io.Discard, nil))

	st, err := Resolve(ctx, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	pace := st.Pace(db, muet, clock)
	if pace == nil || pace(ctx) != 10*time.Minute {
		t.Fatal("sans incident, l'intervalle normal s'applique")
	}

	inscrire := func(at time.Time, level analyze.Level) {
		t.Helper()
		if err := store.SaveEvents(ctx, db, []store.Event{{Time: at, Kind: "etat", Level: int(level), Message: "x"}}); err != nil {
			t.Fatal(err)
		}
	}
	inscrire(now.Add(-5*time.Minute), analyze.Good) // un retour a la normale ne rapproche rien
	if pace(ctx) != 10*time.Minute {
		t.Error("un evenement sans alerte a rapproche les releves")
	}
	inscrire(now.Add(-5*time.Minute), analyze.Warning)
	if pace(ctx) != time.Minute {
		t.Error("une alerte recente doit rapprocher les releves")
	}
	now = now.Add(20 * time.Minute)
	if pace(ctx) != 10*time.Minute {
		t.Error("au-dela d'un quart d'heure, l'intervalle normal doit reprendre")
	}

	off, _ := Resolve(ctx, db, settings.Source{settings.Burst: "false"})
	if off.Pace(db, muet, clock) != nil {
		t.Error("desactive, le rapprochement ne doit pas s'appliquer")
	}
	court, _ := Resolve(ctx, db, settings.Source{settings.Interval: "1m"})
	if court.Pace(db, muet, clock) != nil {
		t.Error("avec un intervalle d'une minute, rien a rapprocher")
	}
}
