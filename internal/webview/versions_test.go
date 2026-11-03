package webview

import (
	"context"
	"github.com/floopich/mire/internal/settings"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestVerifierMaintenant(t *testing.T) {
	tag := "v9.0.0"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"` + tag + `"}`))
	}))
	defer srv.Close()
	db := baseRemplie(t, 2)
	h, svc := monter(t, db, essai{Collecting: true})
	tok := session(t, db)
	c := update.New(db, "v1.7.0", slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.URL = srv.URL
	svc.update = c

	if body := get(t, h, tok, "/reglages/versions").Body.String(); !strings.Contains(body, "Vérifier maintenant") {
		t.Error("bouton absent")
	}
	verifier := func(on bool) string {
		form := url.Values{}
		if on {
			form.Set(settings.UpdateCheck, "true")
		}
		return html.UnescapeString(posteAvec(t, h, tok, "/reglages/versions/tester", form).Body.String())
	}
	// Sans attendre la verification du jour : la nouvelle version est vue.
	if body := verifier(true); !strings.Contains(body, "Vérification faite : une nouvelle version est disponible.") ||
		!strings.Contains(body, "Mire 9.0.0 est disponible.") {
		t.Error("nouvelle version non trouvée")
	}
	tag = "v1.7.0"
	if body := verifier(true); !strings.Contains(body, "Vérification faite : Mire 1.7.0 est la dernière version.") {
		t.Error("à jour : message absent")
	}
	if body := verifier(false); !strings.Contains(body, "Activez « Signaler les nouvelles versions » pour vérifier.") {
		t.Error("désactivé : message absent")
	}
}
