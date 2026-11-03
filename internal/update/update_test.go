package update

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/floopich/mire/internal/store"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"1.6.1", "v1.6.0", true}, {"1.10.0", "1.9.9", true}, {"2.0.0", "1.99.99", true},
		{"1.6.0", "v1.6.0", false}, {"1.5.9", "1.6.0", false},
		{"1.6.1", "dev", false}, {"1.6.1", "main-98b4393", false}, {"1.6.1", "v1.6.0+dirty", false},
		// Versions de test : rc1, rc2, rc10, puis la version finale.
		{"1.6.1-rc1", "1.6.0", true}, {"1.6.1-rc2", "1.6.1-rc1", true}, {"1.6.1-rc10", "1.6.1-rc2", true},
		{"1.6.1", "1.6.1-rc10", true}, {"1.6.1-rc1", "1.6.1", false}, {"1.6.1-rc1", "1.6.1-rc1", false},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestVerification(t *testing.T) {
	tag := "v1.6.1"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "Mire" {
			t.Errorf("User-Agent %q", r.Header.Get("User-Agent"))
		}
		_, _ = w.Write([]byte(`{"tag_name":"` + tag + `","html_url":"https://exemple.invalid/piege","body":"…"}`))
	}))
	defer srv.Close()
	db, err := store.Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	c := New(db, "v1.6.0", slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.URL = srv.URL

	for range 2 {
		if err := c.Check(ctx); err != nil {
			t.Fatal(err)
		}
	}
	r, newer, checked, err := c.Status()
	if !newer || r.Version != "1.6.1" || r.URL != "https://github.com/floopich/mire/releases/tag/v1.6.1" || checked.IsZero() || err != nil {
		t.Fatalf("%+v %v %v %v", r, newer, checked, err)
	}
	evs, _ := store.EventsAfter(ctx, db, 0, 10)
	if len(evs) != 1 || evs[0].Message != "Nouvelle version de Mire : 1.6.1." {
		t.Errorf("journal : %+v", evs)
	}

	// Deja a jour : rien au journal.
	c.Current = "v1.6.1"
	if err := c.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if _, newer, _, _ := c.Status(); newer {
		t.Error("à jour, aucune nouvelle version")
	}

	// Reponse inattendue : une erreur, jamais un lien.
	tag = "v1.7.0<script>"
	if err := c.Check(ctx); err == nil {
		t.Error("numéro de version invalide accepté")
	}
}

func TestCanalDeTest(t *testing.T) {
	stable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v1.8.0"}`))
	}))
	defer stable.Close()
	list := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"tag_name":"v1.9.0-rc3","draft":true},{"tag_name":"v1.9.0-rc2"},{"tag_name":"v1.9.0-rc1"},{"tag_name":"v1.8.0"},{"tag_name":"n'importe"}]`))
	}))
	defer list.Close()
	db, err := store.Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	check := func(current string) (Release, bool) {
		c := New(db, current, slog.New(slog.NewTextHandler(io.Discard, nil)))
		c.URL, c.ListURL = stable.URL, list.URL
		if err := c.Check(context.Background()); err != nil {
			t.Fatal(err)
		}
		r, newer, _, _ := c.Status()
		return r, newer
	}
	// Une version stable ne se voit proposer que des versions stables.
	if r, newer := check("v1.8.0"); newer || r.Version != "1.8.0" {
		t.Errorf("stable : %+v %v", r, newer)
	}
	// Une version de test voit la plus recente, test compris, sauf un brouillon.
	if r, newer := check("v1.9.0-rc1"); !newer || r.Version != "1.9.0-rc2" || r.URL != "https://github.com/floopich/mire/releases/tag/v1.9.0-rc2" {
		t.Errorf("test : %+v %v", r, newer)
	}
}
