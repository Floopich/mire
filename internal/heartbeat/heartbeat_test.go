package heartbeat

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/floopich/mire/internal/docsis"
)

func TestSignalDeVie(t *testing.T) {
	var appels atomic.Int32
	code := atomic.Int32{}
	code.Store(http.StatusOK)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appels.Add(1)
		if r.Header.Get("User-Agent") != "Mire/v9" {
			t.Errorf("User-Agent %q", r.Header.Get("User-Agent"))
		}
		w.WriteHeader(int(code.Load()))
	}))
	defer srv.Close()

	var journal bytes.Buffer
	p := &Pinger{URL: srv.URL + "/api/push/jeton-secret", UserAgent: "Mire/v9",
		Logger: slog.New(slog.NewTextHandler(&journal, nil))}
	p.Recorded(context.Background(), docsis.Snapshot{})
	deadline := time.Now().Add(2 * time.Second)
	for appels.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if appels.Load() != 1 {
		t.Fatalf("%d appels après un relevé", appels.Load())
	}

	// Un refus est journalise une fois, sans le jeton du chemin.
	code.Store(http.StatusNotFound)
	for range 2 {
		if err := p.Ping(context.Background()); err == nil {
			t.Error("refus non signalé")
		}
	}
	if n := strings.Count(journal.String(), "signal de vie refusé"); n != 1 {
		t.Errorf("%d lignes de refus :\n%s", n, journal.String())
	}
	if strings.Contains(journal.String(), "jeton-secret") {
		t.Errorf("le jeton figure au journal :\n%s", journal.String())
	}
	code.Store(http.StatusOK)
	if err := p.Ping(context.Background()); err != nil || !strings.Contains(journal.String(), "de nouveau accepté") {
		t.Errorf("rétablissement : %v\n%s", err, journal.String())
	}

	// Un echec du relevé n'envoie rien.
	avant := appels.Load()
	p.Failed(context.Background(), nil)
	if appels.Load() != avant {
		t.Error("signal envoyé après un échec")
	}
}

func TestSignalDeVieInjoignable(t *testing.T) {
	var journal bytes.Buffer
	p := &Pinger{URL: "http://127.0.0.1:1/ping/jeton-secret", Logger: slog.New(slog.NewTextHandler(&journal, nil))}
	if err := p.Ping(context.Background()); err == nil || strings.Contains(err.Error(), "jeton-secret") {
		t.Errorf("erreur : %v", err)
	}
	if strings.Contains(journal.String(), "jeton-secret") {
		t.Errorf("le jeton figure au journal :\n%s", journal.String())
	}
}
