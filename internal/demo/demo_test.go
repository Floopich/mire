package demo

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/events"
	"github.com/floopich/mire/internal/operator"
	"github.com/floopich/mire/internal/store"
)

func TestHistoriqueDeDemonstration(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	profile, err := operator.Load("voo")
	if err != nil {
		t.Fatal(err)
	}
	origin := time.Date(2026, 10, 27, 21, 0, 0, 0, time.UTC)
	l := New(origin)
	rec := &events.Recorder{DB: db, Profile: profile, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		// Comme le service : les erreurs comptees sur la fenetre du profil.
		Analyze: func(ctx context.Context, snap docsis.Snapshot) (analyze.Health, error) {
			totals, err := store.ErrorTotals(ctx, db, snap.Timestamp.Add(-profile.Thresholds.Errors.Window()), snap.Timestamp)
			if err != nil {
				return analyze.Health{}, err
			}
			opts := analyze.Options{Errors: map[int]analyze.ErrorCount{}}
			for id, e := range totals {
				opts.Errors[id] = analyze.ErrorCount{Corrected: e.Corrected, Uncorrected: e.Uncorrected}
			}
			return analyze.SnapshotWith(snap, profile, opts), nil
		}}
	start := time.Now()
	if err := l.Fill(ctx, db, rec); err != nil {
		t.Fatal(err)
	}
	t.Logf("historique écrit en %v", time.Since(start))

	evs, err := store.EventsBetween(ctx, db, origin.AddDate(0, 0, -Days), origin)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, e := range evs {
		kinds[e.Kind]++
	}
	t.Logf("journal : %v", kinds)
	for _, k := range []string{events.KindState, events.KindRestart, events.KindOutage, events.KindErrors, store.NoteKind} {
		if kinds[k] == 0 {
			t.Errorf("aucun événement %q", k)
		}
	}
	// La ligne finit en bon etat.
	snap, err := store.LatestSnapshot(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if h := analyze.Snapshot(snap, profile); h.Level != analyze.Good {
		t.Errorf("dernier relevé : %v", h.Level)
	}
	if tests, _ := store.SpeedTests(ctx, db, origin.AddDate(0, 0, -Days), origin); len(tests) < 1000 {
		t.Errorf("%d mesures de débit", len(tests))
	}
}
