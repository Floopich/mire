package service

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/events"
	"github.com/floopich/mire/internal/modem"
	"github.com/floopich/mire/internal/store"
)

type modemRedemarrable struct {
	err   error
	appel int
}

func (m *modemRedemarrable) Snapshot(context.Context) (docsis.RawSnapshot, error) {
	return docsis.RawSnapshot{}, nil
}

func (m *modemRedemarrable) Restart(context.Context) error {
	m.appel++
	return m.err
}

type modemSimple struct{}

func (modemSimple) Snapshot(context.Context) (docsis.RawSnapshot, error) {
	return docsis.RawSnapshot{}, nil
}

func TestRedemarrageDuModem(t *testing.T) {
	db := base(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	now := time.Date(2026, 10, 27, 21, 0, 0, 0, time.UTC)
	horloge := func() time.Time { return now }

	if got := RestartModem(ctx, db, modemSimple{}, "Mire", horloge, log); got != RestartUnavailable {
		t.Errorf("modem sans redémarrage : %q", got)
	}
	if got := RestartModem(ctx, db, nil, "Mire", horloge, log); got != RestartUnavailable {
		t.Errorf("sans modem : %q", got)
	}

	m := &modemRedemarrable{}
	if got := RestartModem(ctx, db, m, "Home Assistant", horloge, log); got != RestartAsked {
		t.Fatalf("demande : %q", got)
	}
	e, ok, _ := store.LastEvent(ctx, db, events.KindRestartAsked)
	if !ok || e.Message != "Redémarrage du modem demandé depuis Home Assistant." {
		t.Errorf("journal : %+v", e)
	}

	// Une seconde demande dans les 10 minutes n'atteint pas le modem.
	now = now.Add(5 * time.Minute)
	if got := RestartModem(ctx, db, m, "Mire", horloge, log); got != RestartTooSoon || m.appel != 1 {
		t.Errorf("seconde demande : %q, %d appels", got, m.appel)
	}

	// Passe le delai, un refus est journalise, une panne ne l'est pas.
	now = now.Add(RestartGuard)
	m.err = modem.ErrRestartRefused
	if got := RestartModem(ctx, db, m, "Mire", horloge, log); got != RestartRefused || !got.Noted() {
		t.Errorf("refus : %q", got)
	}
	now = now.Add(RestartGuard)
	m.err = modem.ErrAuth
	if got := RestartModem(ctx, db, m, "Mire", horloge, log); got != RestartFailed || got.Noted() {
		t.Errorf("panne : %q", got)
	}
	if last := LastRestartAsked(ctx, db); !last.Equal(now.Add(-RestartGuard)) {
		t.Errorf("dernière demande %v", last)
	}
}
