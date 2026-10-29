package status

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/events"
	"github.com/floopich/mire/internal/operator"
	"github.com/floopich/mire/internal/store"
)

func TestLecture(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	raw := docsis.RawSnapshot{DOCSIS: "3.1", Downstream: []docsis.RawDownstream{
		{ChannelID: 1, Modulation: "256QAM", Frequency: "442 MHz", PowerLevel: "12", MSE: "-40"}}}
	if _, err := store.SaveSnapshot(ctx, db, raw.Normalize(now.Add(-20*time.Minute))); err != nil {
		t.Fatal(err)
	}
	outage := func(kind string, start, end time.Time) {
		id, err := store.OpenOutage(ctx, db, kind, start, "")
		if err != nil {
			t.Fatal(err)
		}
		if !end.IsZero() {
			if err := store.CloseOutage(ctx, db, id, end); err != nil {
				t.Fatal(err)
			}
		}
	}
	outage("ligne", now.Add(-10*24*time.Hour), now.Add(-10*24*time.Hour+time.Hour))
	outage("ligne", now.Add(-5*time.Hour), now.Add(-4*time.Hour-30*time.Minute))
	outage("local", now.Add(-time.Hour), now.Add(-50*time.Minute)) // ecartee
	if err := store.SaveEvents(ctx, db, []store.Event{{Time: now.Add(-3 * time.Hour), Kind: events.KindRestart, Message: "x"}}); err != nil {
		t.Fatal(err)
	}
	p, _ := operator.Load("voo")
	s, err := Read(ctx, db, Config{Interval: 10 * time.Minute, Now: func() time.Time { return now },
		Analyze: func(_ context.Context, snap docsis.Snapshot) (analyze.Health, error) {
			return analyze.Snapshot(snap, p), nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	if !s.Fresh || s.Outages24h != 1 || s.Outages30d != 2 {
		t.Errorf("frais %v, coupures %d/%d", s.Fresh, s.Outages24h, s.Outages30d)
	}
	if !s.HasLastOutage || !s.LastOutage.Start.Equal(now.Add(-5*time.Hour)) {
		t.Errorf("dernière coupure : %+v (le réseau local doit être écarté)", s.LastOutage)
	}
	if !s.LastRestart.Equal(now.Add(-3 * time.Hour)) {
		t.Errorf("dernier redémarrage : %v", s.LastRestart)
	}
	if d := s.Diagnosis(); !strings.HasPrefix(d, "Ligne critique : canal descendant 1, puissance descendante 12,0") {
		t.Errorf("diagnostic : %q", d)
	}

	// Releve trop ancien : le modem ne repond plus.
	late, err := Read(ctx, db, Config{Interval: 10 * time.Minute, Now: func() time.Time { return now.Add(2 * time.Hour) },
		Analyze: func(_ context.Context, snap docsis.Snapshot) (analyze.Health, error) {
			return analyze.Snapshot(snap, p), nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	if late.Fresh {
		t.Error("un relevé de plus de 30 minutes est encore jugé frais")
	}
}
