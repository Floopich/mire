package collect

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/store"
)

type sourceFausse struct {
	mu      sync.Mutex
	appels  int
	echecs  int // les N premiers appels echouent
	panneur bool
}

func (s *sourceFausse) Snapshot(context.Context) (docsis.RawSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appels++
	if s.appels <= s.echecs {
		return docsis.RawSnapshot{}, errors.New("modem injoignable")
	}
	if s.panneur {
		return docsis.RawSnapshot{}, errors.New("panne permanente")
	}
	return docsis.RawSnapshot{
		DOCSIS: "3.1",
		Downstream: []docsis.RawDownstream{
			{ChannelID: 1, PowerLevel: "1.5", MSE: "-38.9", Modulation: "256QAM"},
		},
		Upstream: []docsis.RawUpstream{{ChannelID: 5, PowerLevel: "45.5"}},
	}, nil
}

func (s *sourceFausse) nbAppels() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appels
}

func baseTest(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatalf("ouverture: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func muet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestUnReleveEstPrisImmediatement(t *testing.T) {
	// Attendre l'intervalle complet avant le moindre releve donnerait
	// l'impression d'un demarrage rate.
	src := &sourceFausse{}
	db := baseTest(t)
	c := New(src, db, Options{Interval: time.Hour, Logger: muet()})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()

	deadline := time.After(2 * time.Second)
	for src.nbAppels() == 0 {
		select {
		case <-deadline:
			t.Fatal("aucun releve dans les deux secondes suivant le demarrage")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	cancel()
	<-done

	var n int
	if err := db.QueryRow("SELECT count(*) FROM readings").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d releves enregistres, attendu 1", n)
	}
}

func TestUnEchecNInterromptPasLaBoucle(t *testing.T) {
	// Un modem qui redemarre ne doit pas arreter la collecte : c'est
	// precisement le moment qu'on veut documenter.
	src := &sourceFausse{echecs: 1}
	db := baseTest(t)
	c := New(src, db, Options{Interval: 20 * time.Millisecond, Logger: muet()})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_ = c.Run(ctx)

	if src.nbAppels() < 2 {
		t.Fatalf("%d appels, la boucle s'est arretee au premier echec", src.nbAppels())
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM readings").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Error("aucun releve enregistre apres reprise")
	}
}

func TestPanneContinueNEcritRien(t *testing.T) {
	src := &sourceFausse{panneur: true}
	db := baseTest(t)
	c := New(src, db, Options{Interval: 20 * time.Millisecond, Logger: muet()})

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_ = c.Run(ctx)

	var n int
	if err := db.QueryRow("SELECT count(*) FROM readings").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d releves ecrits alors que la source echoue toujours", n)
	}
}

func TestAnnulationArreteProprement(t *testing.T) {
	src := &sourceFausse{}
	c := New(src, baseTest(t), Options{Interval: time.Hour, Logger: muet()})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("attendu context.Canceled, obtenu %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("la boucle ne s'est pas arretee apres annulation")
	}
}

func TestValeursParDefaut(t *testing.T) {
	c := New(&sourceFausse{}, baseTest(t), Options{})
	if c.opts.Interval != 10*time.Minute {
		t.Errorf("intervalle par defaut %v, attendu 10m", c.opts.Interval)
	}
	if c.opts.Retention != store.DefaultRetention {
		t.Errorf("retention par defaut %+v", c.opts.Retention)
	}
}

func TestCollectOnceRemonteLErreur(t *testing.T) {
	// A la difference de la boucle, le releve unique sert au diagnostic :
	// l'erreur doit revenir a l'appelant, pas seulement dans les logs.
	c := New(&sourceFausse{panneur: true}, baseTest(t), Options{Logger: muet()})
	if _, err := c.CollectOnce(context.Background()); err == nil {
		t.Error("un echec de releve unique doit remonter")
	}
}

type observateur struct {
	releves, echecs int
}

func (o *observateur) Recorded(context.Context, docsis.Snapshot) { o.releves++ }
func (o *observateur) Failed(context.Context, error)             { o.echecs++ }

func TestLObservateurSuitChaqueReleve(t *testing.T) {
	src := &sourceFausse{echecs: 1}
	obs := &observateur{}
	c := New(src, baseTest(t), Options{Logger: muet(), Observer: obs})
	ctx := context.Background()
	c.collectOnce(ctx) // echec
	c.collectOnce(ctx) // releve
	if obs.echecs != 1 || obs.releves != 1 {
		t.Errorf("observateur : %d echecs, %d releves, attendu 1 et 1", obs.echecs, obs.releves)
	}
}

func TestLeRythmeSuitPace(t *testing.T) {
	// Intervalle d'une heure, mais Pace demande 10 ms : la boucle doit
	// suivre Pace, recalcule apres chaque releve.
	src := &sourceFausse{}
	db := baseTest(t)
	c := New(src, db, Options{Interval: time.Hour, Logger: muet(),
		Pace: func(context.Context) time.Duration { return 10 * time.Millisecond }})
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_ = c.Run(ctx)
	if src.nbAppels() < 5 {
		t.Errorf("%d appels : le rythme de Pace n'est pas suivi", src.nbAppels())
	}
}

func TestUnReveilRecalculeLAttente(t *testing.T) {
	// Une heure d'attente en cours ; le reveil la remplace par le rythme
	// rapproche, sans attendre l'heure.
	src := &sourceFausse{}
	db := baseTest(t)
	var mu sync.Mutex
	pace := time.Hour
	wake := make(chan struct{}, 1)
	c := New(src, db, Options{Interval: time.Hour, Logger: muet(), Wake: wake,
		Pace: func(context.Context) time.Duration { mu.Lock(); defer mu.Unlock(); return pace }})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() { _ = c.Run(ctx) }()
	for src.nbAppels() < 1 {
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	pace = 10 * time.Millisecond
	mu.Unlock()
	wake <- struct{}{}
	deadline := time.Now().Add(time.Second)
	for src.nbAppels() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("le réveil n'a pas rapproché le relevé suivant")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
