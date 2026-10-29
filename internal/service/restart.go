package service

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/events"
	"github.com/floopich/mire/internal/modem"
	"github.com/floopich/mire/internal/store"
)

// Le redemarrage du modem se demande depuis la page Modem ou depuis Home
// Assistant : les deux passent ici, avec le meme garde-fou et la meme trace
// au journal.

// Outcome est l'issue d'une demande de redemarrage.
type Outcome string

const (
	// RestartAsked : le modem a accepte la demande.
	RestartAsked Outcome = "demande"
	// RestartNoAnswer : la demande est partie sans confirmation ; le modem
	// redemarre sans doute.
	RestartNoAnswer Outcome = "sans-reponse"
	// RestartRefused : le modem a repondu par un refus.
	RestartRefused Outcome = "refuse"
	// RestartFailed : le modem n'a pas pu etre joint.
	RestartFailed Outcome = "erreur"
	// RestartTooSoon : une demande vient d'etre faite.
	RestartTooSoon Outcome = "trop-tot"
	// RestartUnavailable : le pilote ne sait pas redemarrer ce modem.
	RestartUnavailable Outcome = "indisponible"
)

// Noted dit si la demande figure au journal.
func (o Outcome) Noted() bool {
	return o == RestartAsked || o == RestartNoAnswer || o == RestartRefused
}

const (
	// RestartGuard est le delai pendant lequel une nouvelle demande est
	// refusee : le temps que le modem revienne.
	RestartGuard = 10 * time.Minute
	// restartTimeout borne la demande.
	restartTimeout = 45 * time.Second
)

// restartMu evite deux demandes simultanees, d'ou qu'elles viennent.
var restartMu sync.Mutex

// LastRestartAsked renvoie la date de la derniere demande, zero sans demande.
func LastRestartAsked(ctx context.Context, db *sql.DB) time.Time {
	e, ok, err := store.LastEvent(ctx, db, events.KindRestartAsked)
	if err != nil || !ok {
		return time.Time{}
	}
	return e.Time
}

// RestartModem demande le redemarrage du modem et inscrit la demande au
// journal. from dit d'ou elle vient, en clair : « Mire », « Home Assistant ».
func RestartModem(ctx context.Context, db *sql.DB, d modem.Driver, from string, now func() time.Time, logger *slog.Logger) Outcome {
	rs, ok := d.(modem.Restarter)
	if d == nil || !ok {
		return RestartUnavailable
	}
	restartMu.Lock()
	defer restartMu.Unlock()
	if last := LastRestartAsked(ctx, db); !last.IsZero() && now().Sub(last) < RestartGuard {
		logger.Info("redémarrage du modem écarté : une demande vient d'être faite", "depuis", from)
		return RestartTooSoon
	}

	rctx, cancel := context.WithTimeout(ctx, restartTimeout)
	defer cancel()
	err := rs.Restart(rctx)
	var out Outcome
	var msg string
	switch {
	case err == nil:
		out, msg = RestartAsked, "Redémarrage du modem demandé depuis "+from+"."
		logger.Info("redémarrage du modem demandé", "depuis", from)
	case errors.Is(err, modem.ErrNoAnswer):
		out, msg = RestartNoAnswer, "Redémarrage du modem demandé depuis "+from+" ; le modem ne l'a pas confirmé."
		logger.Warn("redémarrage du modem demandé, sans confirmation", "depuis", from, "erreur", err)
	case errors.Is(err, modem.ErrRestartRefused):
		out, msg = RestartRefused, "Redémarrage du modem demandé depuis "+from+" ; le modem l'a refusé."
		logger.Warn("redémarrage du modem refusé", "depuis", from, "erreur", err)
	default:
		logger.Warn("redémarrage du modem impossible", "depuis", from, "erreur", err)
		return RestartFailed
	}
	e := store.Event{Time: now(), Kind: events.KindRestartAsked, Level: int(analyze.Good), Message: msg}
	if err := store.SaveEvents(context.WithoutCancel(ctx), db, []store.Event{e}); err != nil {
		logger.Error("journal", "erreur", err)
	}
	return out
}
