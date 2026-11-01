package webview

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/floopich/mire/internal/update"
)

func TestNouvelleVersionAnnoncee(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v9.0.0"}`))
	}))
	defer srv.Close()
	db := baseRemplie(t, 2)
	h, svc := monter(t, db, essai{Collecting: true})
	tok := session(t, db)

	if body := get(t, h, tok, "/").Body.String(); strings.Contains(body, "est disponible") {
		t.Fatal("désactivé, rien n'est annoncé")
	}
	if body := get(t, h, tok, "/reglages").Body.String(); !strings.Contains(body, "Nouvelles versions") {
		t.Error("rubrique absente")
	}

	c := update.New(db, "v1.6.0", slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.URL = srv.URL
	if err := c.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	svc.st.Update = c
	body := get(t, h, tok, "/").Body.String()
	if !strings.Contains(body, "Mire 9.0.0 est disponible.") || !strings.Contains(body, `href="https://github.com/floopich/mire/releases/tag/v9.0.0"`) {
		t.Error("vue d'ensemble : nouvelle version non annoncée")
	}
	body = get(t, h, tok, "/reglages/versions").Body.String()
	if !strings.Contains(body, "Mire 9.0.0 disponible") || !strings.Contains(body, "docker compose pull") {
		t.Error("réglages : nouvelle version non annoncée")
	}
}
