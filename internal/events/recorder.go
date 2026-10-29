package events

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/modem"
	"github.com/floopich/mire/internal/operator"
	"github.com/floopich/mire/internal/store"
)

// failuresBeforeAlert est le nombre d'echecs consecutifs qui font inscrire
// un modem injoignable : un echec isole, le temps d'un redemarrage du
// serveur web du modem, n'en est pas un.
const failuresBeforeAlert = 2

// Recorder inscrit au journal ce que la collecte observe. La boucle de
// collecte l'appelle apres chaque releve, reussi ou non, depuis une seule
// goroutine.
type Recorder struct {
	DB      *sql.DB
	Profile operator.Profile
	// Analyze qualifie un releve avec les reglages en vigueur.
	Analyze func(context.Context, docsis.Snapshot) (analyze.Health, error)
	Logger  *slog.Logger
	// Now est injectable pour les tests.
	Now func() time.Time

	failures     int
	firstFailure time.Time
}

func (r *Recorder) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Recorder) logger() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.Default()
}

// Recorded compare un releve enregistre au precedent.
func (r *Recorder) Recorded(ctx context.Context, snap docsis.Snapshot) {
	if err := r.recorded(ctx, snap); err != nil {
		r.logger().Error("journal des événements", "erreur", err)
	}
}

func (r *Recorder) recorded(ctx context.Context, snap docsis.Snapshot) error {
	r.failures = 0
	in := Input{Cur: snap, Profile: r.Profile}

	prev, err := store.SnapshotBefore(ctx, r.DB, snap.Timestamp)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return err
	default:
		in.Prev = &prev
	}
	if in.Health, err = r.Analyze(ctx, snap); err != nil {
		return err
	}
	last, ok, err := store.LastEvent(ctx, r.DB, KindState)
	if err != nil {
		return err
	}
	if ok {
		in.LastState = &last
	}

	spike, ok, err := store.LastEvent(ctx, r.DB, KindErrors, KindErrorsEnd)
	if err != nil {
		return err
	}
	if ok && spike.Kind == KindErrors {
		in.OpenSpike = &spike
		totals, err := store.ErrorTotals(ctx, r.DB, spike.Time, snap.Timestamp)
		if err != nil {
			return err
		}
		for _, e := range totals {
			in.SpikeTotal += e.Uncorrected
		}
	}

	var out []store.Event
	// Le modem repond de nouveau : la panne inscrite se referme.
	modemLast, ok, err := store.LastEvent(ctx, r.DB, KindModemDown, KindModemUp)
	if err != nil {
		return err
	}
	if ok && modemLast.Kind == KindModemDown {
		out = append(out, store.Event{Time: snap.Timestamp, Kind: KindModemUp, Level: int(analyze.Good),
			Message: "Le modem répond de nouveau, après " + Duration(snap.Timestamp.Sub(modemLast.Time)) + " sans relevé."})
	}
	out = append(out, Detect(in)...)
	return store.SaveEvents(ctx, r.DB, out)
}

// Failed compte un releve manque.
func (r *Recorder) Failed(ctx context.Context, cause error) {
	r.failures++
	if r.failures == 1 {
		r.firstFailure = r.now().UTC()
	}
	if r.failures != failuresBeforeAlert {
		return
	}
	// Une panne deja inscrite, par exemple avant un redemarrage du service,
	// ne l'est pas deux fois.
	last, ok, err := store.LastEvent(ctx, r.DB, KindModemDown, KindModemUp)
	if err != nil {
		r.logger().Error("journal des événements", "erreur", err)
		return
	}
	if ok && last.Kind == KindModemDown {
		return
	}
	e := store.Event{Time: r.firstFailure, Kind: KindModemDown, Level: int(analyze.Critical),
		Message: "Le modem ne répond plus."}
	if errors.Is(cause, modem.ErrAuth) {
		e.Level = int(analyze.Warning)
		e.Message = "Le modem refuse l'identifiant ou le mot de passe."
	}
	if err := store.SaveEvents(ctx, r.DB, []store.Event{e}); err != nil {
		r.logger().Error("journal des événements", "erreur", err)
	}
}

// Duration rend une duree lisible : « 35 min », « 2 h 10 », « 3 j ».
func Duration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "moins d'une minute"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + " min"
	case d < 48*time.Hour:
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if m == 0 {
			return strconv.Itoa(h) + " h"
		}
		return strconv.Itoa(h) + " h " + twoDigits(m)
	default:
		return strconv.Itoa(int(d.Hours()/24)) + " j"
	}
}

func twoDigits(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}
