package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestGigueCumulee(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	t0 := time.Date(2026, 11, 3, 20, 0, 0, 0, time.UTC)
	f := func(v float64) *float64 { return &v }
	// Deux moities d'une meme tranche : 2 ms sur 10 paires, puis 5 ms sur 30.
	for _, s := range []ProbeSample{
		{Start: t0, Rounds: 11, RTTAvg: f(10), Jitter: f(2), JitterPairs: 10},
		{Start: t0, Rounds: 31, RTTAvg: f(10), Jitter: f(5), JitterPairs: 30},
		{Start: t0.Add(5 * time.Minute), Rounds: 3, Failed: 3},
	} {
		if err := SaveProbeSample(ctx, db, s); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ProbeSamples(ctx, db, t0, t0.Add(time.Hour))
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	if got[0].JitterPairs != 40 || got[0].Jitter == nil || *got[0].Jitter != 4.25 {
		t.Errorf("moyenne pondérée : %v sur %d", got[0].Jitter, got[0].JitterPairs)
	}
	if got[1].Jitter != nil {
		t.Error("tranche sans réussite : pas de gigue")
	}
}
