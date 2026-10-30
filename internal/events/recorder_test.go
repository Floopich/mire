package events

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/modem"
	"github.com/floopich/mire/internal/store"
)

func recorder(t *testing.T) (*Recorder, *sql.DB, *time.Time) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := t0
	r := &Recorder{DB: db, Profile: voo(t),
		Analyze: func(context.Context, docsis.Snapshot) (analyze.Health, error) {
			return analyze.Health{Qualified: true, Level: analyze.Good}, nil
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    func() time.Time { return now },
	}
	return r, db, &now
}

func journal(t *testing.T, db *sql.DB) []store.Event {
	t.Helper()
	list, err := store.Events(context.Background(), db, store.EventFilter{From: time.Unix(0, 0), To: t0.Add(48 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestModemInjoignablePuisRetrouve(t *testing.T) {
	r, db, now := recorder(t)
	ctx := context.Background()
	panne := fmt.Errorf("connexion au modem : %w", errors.New("connection refused"))

	r.Failed(ctx, panne)
	if len(journal(t, db)) != 0 {
		t.Fatal("un echec isole ne doit rien inscrire")
	}
	*now = t0.Add(10 * time.Minute)
	r.Failed(ctx, panne)
	*now = t0.Add(20 * time.Minute)
	r.Failed(ctx, panne)
	list := journal(t, db)
	if len(list) != 1 || list[0].Kind != KindModemDown || !list[0].Time.Equal(t0) {
		t.Fatalf("journal %+v, attendu une panne datee du premier echec", list)
	}

	// Le service redemarre pendant la panne : elle n'est pas inscrite deux fois.
	r2 := *r
	r2.failures = 0
	r2.Failed(ctx, panne)
	r2.Failed(ctx, panne)
	if n := len(journal(t, db)); n != 1 {
		t.Fatalf("%d entrees, la panne a ete inscrite deux fois", n)
	}

	snap := ligne(t0.Add(45*time.Minute), 4, 0, 0)
	if _, err := store.SaveSnapshot(ctx, db, snap); err != nil {
		t.Fatal(err)
	}
	r.Recorded(ctx, snap)
	list = journal(t, db)
	if len(list) != 2 || list[0].Kind != KindModemUp || list[0].Message != "Le modem répond de nouveau, après 45 min sans relevé." {
		t.Errorf("journal %q", fmt.Sprint(list))
	}
}

func TestUnRefusDIdentificationEstDistingue(t *testing.T) {
	r, db, _ := recorder(t)
	ctx := context.Background()
	r.Failed(ctx, modem.ErrAuth)
	r.Failed(ctx, modem.ErrAuth)
	list := journal(t, db)
	if len(list) != 1 || list[0].Level != int(analyze.Warning) {
		t.Errorf("journal %q", fmt.Sprint(list))
	}
}

func TestLeJournalCompareAuReleveEnregistrePrecedent(t *testing.T) {
	r, db, _ := recorder(t)
	ctx := context.Background()
	for i, n := range []int{6, 4} {
		snap := ligne(t0.Add(time.Duration(i)*10*time.Minute), n, 0, 0)
		if _, err := store.SaveSnapshot(ctx, db, snap); err != nil {
			t.Fatal(err)
		}
		r.Recorded(ctx, snap)
	}
	list := journal(t, db)
	if len(list) != 1 || list[0].Message != "Canaux descendants perdus : 5 et 6." {
		t.Errorf("journal %q", fmt.Sprint(list))
	}
}

// tempsDeMarche joue le temps de fonctionnement lu sur le modem.
type tempsDeMarche struct{ up time.Duration }

func (m *tempsDeMarche) Info(context.Context) (modem.Info, error) {
	return modem.Info{Uptime: m.up}, nil
}

func TestUnRedemarrageDemandeSeConstate(t *testing.T) {
	ctx := context.Background()
	demande := func(t *testing.T, db *sql.DB, at time.Time, level analyze.Level) {
		t.Helper()
		e := store.Event{Time: at, Kind: KindRestartAsked, Level: int(level), Message: "Redémarrage du modem demandé depuis Mire."}
		if err := store.SaveEvents(ctx, db, []store.Event{e}); err != nil {
			t.Fatal(err)
		}
	}
	releve := func(t *testing.T, r *Recorder, db *sql.DB, at time.Time, compteur int64) {
		t.Helper()
		snap := ligne(at, 4, compteur, 0)
		if _, err := store.SaveSnapshot(ctx, db, snap); err != nil {
			t.Fatal(err)
		}
		r.Recorded(ctx, snap)
	}
	suivi := func(db *sql.DB) []store.Event {
		var out []store.Event
		for _, e := range journal(t, db) {
			if e.Kind != KindRestartAsked {
				out = append(out, e)
			}
		}
		return out
	}

	t.Run("temps de fonctionnement remis à zéro", func(t *testing.T) {
		r, db, _ := recorder(t)
		m := &tempsDeMarche{up: 50 * time.Hour}
		r.Modem = m
		releve(t, r, db, t0, 100)
		demande(t, db, t0.Add(time.Minute), analyze.Good)
		m.up = 3 * time.Minute
		releve(t, r, db, t0.Add(5*time.Minute), 120)
		releve(t, r, db, t0.Add(6*time.Minute), 130)
		list := suivi(db)
		if len(list) != 1 || list[0].Kind != KindRestart || list[0].Level != int(analyze.Good) ||
			list[0].Message != "Redémarrage du modem confirmé, 4\u00a0min après la demande." {
			t.Errorf("journal %q", fmt.Sprint(list))
		}
	})

	t.Run("compteurs remis à zéro : un seul redémarrage inscrit", func(t *testing.T) {
		r, db, _ := recorder(t)
		r.Modem = &tempsDeMarche{up: 2 * time.Minute}
		releve(t, r, db, t0, 100)
		demande(t, db, t0.Add(time.Minute), analyze.Good)
		releve(t, r, db, t0.Add(4*time.Minute), 0)
		list := suivi(db)
		if len(list) != 1 || list[0].Kind != KindRestart || list[0].Level != int(analyze.Good) {
			t.Errorf("journal %q", fmt.Sprint(list))
		}
	})

	t.Run("le modem n'a pas redémarré", func(t *testing.T) {
		r, db, _ := recorder(t)
		r.Modem = &tempsDeMarche{up: 50 * time.Hour}
		releve(t, r, db, t0, 100)
		demande(t, db, t0.Add(time.Minute), analyze.Good)
		releve(t, r, db, t0.Add(4*time.Minute), 110)
		if list := suivi(db); len(list) != 0 {
			t.Fatalf("trop tôt pour conclure : %+v", list)
		}
		releve(t, r, db, t0.Add(12*time.Minute), 120)
		releve(t, r, db, t0.Add(13*time.Minute), 130)
		list := suivi(db)
		if len(list) != 1 || list[0].Kind != KindRestartMissed || list[0].Message != "Le modem n'a pas redémarré : en marche depuis 2\u00a0j." {
			t.Errorf("journal %q", fmt.Sprint(list))
		}
	})

	t.Run("demande refusée", func(t *testing.T) {
		r, db, _ := recorder(t)
		r.Modem = &tempsDeMarche{up: 50 * time.Hour}
		releve(t, r, db, t0, 100)
		demande(t, db, t0.Add(time.Minute), analyze.Warning)
		releve(t, r, db, t0.Add(12*time.Minute), 120)
		if list := suivi(db); len(list) != 0 {
			t.Errorf("journal %q", fmt.Sprint(list))
		}
	})

	t.Run("sans temps de fonctionnement, les compteurs suffisent", func(t *testing.T) {
		r, db, _ := recorder(t)
		releve(t, r, db, t0, 100)
		demande(t, db, t0.Add(time.Minute), analyze.Good)
		releve(t, r, db, t0.Add(20*time.Minute), 120)
		if list := suivi(db); len(list) != 0 {
			t.Fatalf("rien ne permet de conclure : %+v", list)
		}
		releve(t, r, db, t0.Add(25*time.Minute), 0)
		list := suivi(db)
		if len(list) != 1 || list[0].Message != "Redémarrage du modem confirmé, 24\u00a0min après la demande." {
			t.Errorf("journal %q", fmt.Sprint(list))
		}
	})
}
