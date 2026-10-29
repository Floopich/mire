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
		t.Errorf("journal %+v", list)
	}
}

func TestUnRefusDIdentificationEstDistingue(t *testing.T) {
	r, db, _ := recorder(t)
	ctx := context.Background()
	r.Failed(ctx, modem.ErrAuth)
	r.Failed(ctx, modem.ErrAuth)
	list := journal(t, db)
	if len(list) != 1 || list[0].Level != int(analyze.Warning) {
		t.Errorf("journal %+v", list)
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
		t.Errorf("journal %+v", list)
	}
}
