package speedtest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/store"
)

// mesure rend une mesure de l'API, telle que Speedtest Tracker l'ecrit.
func mesure(at time.Time, status string) string {
	return fmt.Sprintf(`{"id":1,"service":"ookla","ping":12.5,"download":118750000,"upload":6000000,
		"status":%q,"created_at":%q,"data":{"timestamp":%q,"packetLoss":0,"ping":{"jitter":1.2},
		"server":{"name":"Orange Belgium","location":"Brussels"}}}`,
		status, at.Format(time.DateTime), at.Format(time.RFC3339))
}

func instance(t *testing.T, pages [][]string) (*httptest.Server, *[]string) {
	t.Helper()
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer jeton" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/api/v1/results" || r.Header.Get("Accept") != "application/json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		asked = append(asked, r.URL.Query().Get("page[number]"))
		var n int
		_, _ = fmt.Sscan(r.URL.Query().Get("page[number]"), &n)
		var data []string
		if n >= 1 && n <= len(pages) {
			data = pages[n-1]
		}
		fmt.Fprintf(w, `{"data":[%s],"meta":{"current_page":%d,"last_page":%d}}`, strings.Join(data, ","), n, len(pages))
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}

func TestLectureDesMesures(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	t0 := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	srv, asked := instance(t, [][]string{
		{mesure(t0.Add(3*time.Hour), "completed"), mesure(t0.Add(2*time.Hour), "failed")},
		{mesure(t0.Add(time.Hour), "completed"), mesure(t0, "completed")},
	})
	f := &Fetcher{DB: db, Source: NewClient(srv.URL+"/", "jeton")}
	if n, err := f.Update(ctx); err != nil || n != 3 {
		t.Fatalf("première lecture : %d mesures, %v", n, err)
	}
	got, _ := store.SpeedTests(ctx, db, t0, t0.Add(4*time.Hour))
	if len(got) != 3 || got[0].Download != 950 || got[0].Upload != 48 || *got[0].Ping != 12.5 ||
		*got[0].Jitter != 1.2 || got[0].Server != "Orange Belgium (Brussels)" || !got[2].Time.Equal(t0.Add(3*time.Hour)) {
		t.Errorf("mesures : %+v", got)
	}

	// Ensuite, la lecture s'arrete a la premiere mesure deja connue.
	*asked = nil
	if n, err := f.Update(ctx); err != nil || n != 0 || len(*asked) != 1 {
		t.Errorf("lecture suivante : %d mesures, %v, pages %v", n, err, *asked)
	}
	if last, err := f.Status(); last.IsZero() || err != nil {
		t.Errorf("état : %v, %v", last, err)
	}
}

func TestErreursDeLInstance(t *testing.T) {
	srv, _ := instance(t, nil)
	ctx := context.Background()
	for c, msg := range map[Client]string{
		NewClient(srv.URL, "faux"):           "refuse le jeton",
		NewClient(srv.URL+"/autre", "jeton"): "aucune API",
		NewClient("", "jeton"):               "adresse de Speedtest Tracker non renseignée",
		NewClient(srv.URL, ""):               "jeton d'API non renseigné",
		NewClient("http://127.0.0.1:1", "x"): "ne répond pas",
	} {
		if _, err := c.Since(ctx, time.Time{}, 1); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%s : %v, attendu %q", c.URL, err, msg)
		}
	}
}
