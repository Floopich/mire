package probe

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/floopich/mire/internal/events"
	"github.com/floopich/mire/internal/store"
)

// reseau simule les cibles : celles marquees joignables acceptent la
// connexion, les autres la refusent.
type reseau struct {
	mu    sync.Mutex
	up    map[string]bool
	delay time.Duration
}

func (r *reseau) set(addr string, up bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.up[addr] = up
}

func (r *reseau) dial(ctx context.Context, _, addr string) (net.Conn, error) {
	r.mu.Lock()
	up, delay := r.up[addr], r.delay
	r.mu.Unlock()
	if !up {
		return nil, errors.New("connexion refusée")
	}
	time.Sleep(delay)
	a, b := net.Pipe()
	_ = b.Close()
	return a, nil
}

type horloge struct{ t time.Time }

func (h *horloge) now() time.Time { return h.t }

func sonde(t *testing.T, modem string) (*Prober, *reseau, *horloge, *sql.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	r := &reseau{up: map[string]bool{"a:443": true, "b:443": true, "modem:80": true}}
	h := &horloge{t: time.Date(2026, 10, 25, 20, 0, 0, 0, time.UTC)}
	p := New(db, Config{Targets: []string{"a:443", "b:443"}, Modem: modem, Interval: 30 * time.Second,
		Dial: r.dial, Now: h.now, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	return p, r, h, db
}

func tour(p *Prober, h *horloge, n int) {
	for range n {
		p.Round(context.Background())
		h.t = h.t.Add(30 * time.Second)
	}
}

func journal(t *testing.T, db *sql.DB) []store.Event {
	t.Helper()
	list, err := store.Events(context.Background(), db, store.EventFilter{From: time.Unix(0, 0), To: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func TestUneCibleSuffit(t *testing.T) {
	p, r, h, db := sonde(t, "modem:80")
	r.set("a:443", false)
	tour(p, h, 5)
	if len(journal(t, db)) != 0 {
		t.Error("une cible en panne n'est pas une coupure de la ligne")
	}
}

func TestCoupureDeLaLigne(t *testing.T) {
	p, r, h, db := sonde(t, "modem:80")
	ctx := context.Background()
	tour(p, h, 2)
	debut := h.t
	r.set("a:443", false)
	r.set("b:443", false)

	tour(p, h, 2)
	if len(journal(t, db)) != 0 {
		t.Fatal("deux tentatives manquees ne font pas encore une coupure")
	}
	tour(p, h, 1)
	o, ok, err := store.CurrentOutage(ctx, db)
	if err != nil || !ok || o.Kind != KindLine || !o.Start.Equal(debut) {
		t.Fatalf("coupure %+v %v %v, attendu une coupure de la ligne datee de la premiere tentative manquee", o, ok, err)
	}

	tour(p, h, 10)
	r.set("b:443", true)
	fin := h.t
	tour(p, h, 1)
	if _, ok, _ := store.CurrentOutage(ctx, db); ok {
		t.Fatal("la coupure n'est pas refermee")
	}
	list, _ := store.Outages(ctx, db, debut, fin)
	if len(list) != 1 || !list[0].End.Equal(fin) || list[0].Duration(fin) != 6*time.Minute+30*time.Second {
		t.Errorf("coupures %+v", list)
	}
	ev := journal(t, db)
	if len(ev) != 2 || ev[0].Kind != events.KindOutageEnd || ev[1].Kind != events.KindOutage {
		t.Fatalf("journal %+v", ev)
	}
	if ev[0].Message != "Fin de coupure de la ligne, après 6 min." {
		t.Errorf("message %q", ev[0].Message)
	}
}

func TestReseauLocalCoupe(t *testing.T) {
	p, r, h, db := sonde(t, "modem:80")
	for _, a := range []string{"a:443", "b:443", "modem:80"} {
		r.set(a, false)
	}
	tour(p, h, 3)
	o, ok, _ := store.CurrentOutage(context.Background(), db)
	if !ok || o.Kind != KindLocal {
		t.Errorf("coupure %+v, attendu le reseau local", o)
	}
}

func TestSansModemConnuLaCauseResteIndeterminee(t *testing.T) {
	p, r, h, db := sonde(t, "")
	r.set("a:443", false)
	r.set("b:443", false)
	tour(p, h, 3)
	if o, ok, _ := store.CurrentOutage(context.Background(), db); !ok || o.Kind != KindInternet {
		t.Errorf("coupure %+v", o)
	}
}

func TestLesMesuresSAgregentParTranche(t *testing.T) {
	p, r, h, db := sonde(t, "modem:80")
	ctx := context.Background()
	r.delay = 2 * time.Millisecond
	tour(p, h, 10) // 5 minutes : une tranche complete
	r.set("a:443", false)
	r.set("b:443", false)
	tour(p, h, 1) // premiere tentative de la tranche suivante, manquee
	p.flush(ctx)

	list, err := store.ProbeSamples(ctx, db, time.Unix(0, 0), h.t)
	if err != nil || len(list) != 2 {
		t.Fatalf("tranches %+v (%v)", list, err)
	}
	if list[0].Rounds != 10 || list[0].Failed != 0 || list[0].RTTAvg == nil || *list[0].RTTAvg < 2 {
		t.Errorf("premiere tranche %+v", list[0])
	}
	if list[1].Rounds != 1 || list[1].Failed != 1 || list[1].RTTAvg != nil {
		t.Errorf("seconde tranche %+v", list[1])
	}

	// Une tranche reprise apres un redemarrage se complete.
	extra := 4.0
	if err := store.SaveProbeSample(ctx, db, store.ProbeSample{Start: list[1].Start, Rounds: 1, RTTMin: &extra, RTTAvg: &extra, RTTMax: &extra}); err != nil {
		t.Fatal(err)
	}
	list, _ = store.ProbeSamples(ctx, db, list[1].Start, list[1].Start)
	if s := list[0]; s.Rounds != 2 || s.Failed != 1 || s.RTTAvg == nil || *s.RTTAvg != 4 {
		t.Errorf("tranche completee %+v", s)
	}
}

func TestUneCoupureInterrompueParUnArretEstRefermee(t *testing.T) {
	p, r, h, db := sonde(t, "modem:80")
	ctx := context.Background()
	r.set("a:443", false)
	r.set("b:443", false)
	tour(p, h, 12)
	p.flush(ctx)

	// Le service redemarre deux heures plus tard.
	h.t = h.t.Add(2 * time.Hour)
	p2 := New(db, p.cfg)
	p2.resume(ctx)
	if _, ok, _ := store.CurrentOutage(ctx, db); ok {
		t.Fatal("la coupure restee ouverte n'est pas refermee")
	}
	list, _ := store.Outages(ctx, db, time.Unix(0, 0), h.t)
	if len(list) != 1 || list[0].Duration(h.t) > 10*time.Minute {
		t.Errorf("coupure %+v : elle ne doit pas durer jusqu'au redemarrage", list)
	}
	if ev := journal(t, db); ev[0].Kind != events.KindOutageEnd {
		t.Errorf("journal %+v", ev)
	}
}
