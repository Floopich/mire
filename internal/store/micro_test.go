package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestMicroCoupures(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	t0 := time.Date(2026, 11, 1, 20, 0, 0, 0, time.UTC)

	since, err := MicroSince(ctx, db)
	if err != nil || since.IsZero() || time.Since(since) > time.Minute {
		t.Fatalf("début de la détection : %v, %v", since, err)
	}
	m := MicroCut{Start: t0, Min: 5 * time.Second, Max: 15 * time.Second, Kind: "ligne"}
	for range 2 {
		if err := SaveMicroCut(ctx, db, m); err != nil {
			t.Fatal(err)
		}
	}
	old := MicroCut{Start: t0.AddDate(0, 0, -40), Min: 10 * time.Second, Max: 20 * time.Second, Kind: "local"}
	if err := SaveMicroCut(ctx, db, old); err != nil {
		t.Fatal(err)
	}
	list, err := MicroCuts(ctx, db, t0.Add(-time.Hour), t0.Add(time.Hour))
	if err != nil || len(list) != 1 || list[0] != m {
		t.Fatalf("%+v, %v", list, err)
	}

	if _, err := Purge(ctx, db, Retention{Days: 30}, t0); err != nil {
		t.Fatal(err)
	}
	if list, _ := MicroCuts(ctx, db, time.Unix(0, 0), t0.Add(time.Hour)); len(list) != 1 {
		t.Errorf("la conservation doit retirer les micro-coupures anciennes : %+v", list)
	}

	if err := SetMicroSince(ctx, db, t0.AddDate(0, 0, -90)); err != nil {
		t.Fatal(err)
	}
	if since, _ := MicroSince(ctx, db); !since.Equal(t0.AddDate(0, 0, -90)) {
		t.Errorf("début : %v", since)
	}
}
