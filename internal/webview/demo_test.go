package webview

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestModeDemonstration(t *testing.T) {
	db := baseRemplie(t, 1)
	h, _ := monter(t, db, essai{Collecting: true, Demo: true})

	// Sans session : la demonstration s'ouvre directement.
	rec := get(t, h, "", "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Démonstration : données fictives.") || strings.Contains(body, "/deconnexion") {
		t.Error("bandeau absent ou déconnexion proposée")
	}
	for _, page := range []string{"/connexion", "/installation"} {
		if rec := get(t, h, "", page); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
			t.Errorf("%s : HTTP %d vers %q", page, rec.Code, rec.Header().Get("Location"))
		}
	}

	// Hors demonstration, rien ne change.
	h, _ = monter(t, db, essai{Collecting: true})
	if rec := get(t, h, "", "/"); rec.Code != http.StatusSeeOther {
		t.Errorf("hors démo sans session : HTTP %d", rec.Code)
	}
}

func TestDemonstrationDepuisLAccueil(t *testing.T) {
	defer func(d time.Duration) { quickWait = d }(quickWait)
	quickWait = 20 * time.Millisecond
	db := baseRemplie(t, 1)
	demoDB := baseRemplie(t, 1)
	demoH, _ := monter(t, demoDB, essai{Collecting: true, Demo: true, DemoExit: true})
	arrets := 0
	lent := make(chan struct{})
	h, _ := monter(t, db, essai{SansMotDePasse: true, StartDemo: func(context.Context) (http.Handler, func(), error) {
		<-lent
		return demoH, func() { arrets++ }, nil
	}})

	// L'accueil propose la demonstration avant l'assistant.
	body := get(t, h, "", "/installation").Body.String()
	if !strings.Contains(body, `action="/demo"`) || !strings.Contains(body, "/installation?etape=mot-de-passe") {
		t.Fatalf("accueil : %s", body)
	}
	if body := get(t, h, "", "/installation?etape=mot-de-passe").Body.String(); !strings.Contains(body, `name="nouveau"`) {
		t.Error("l'étape du mot de passe reste accessible")
	}

	// La preparation est suivie, puis la demonstration s'ouvre.
	rec := posteAvec(t, h, "", "/demo", url.Values{})
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/demo?preparation=") || !strings.Contains(rec.Header().Get("Set-Cookie"), "mire_demo=1") {
		t.Fatalf("démarrage : %q %v", loc, rec.Header())
	}
	cookie := &http.Cookie{Name: "mire_demo", Value: "1"}
	if body := avecCookie(t, h, loc, cookie).Body.String(); !strings.Contains(body, "Préparation de la démonstration") {
		t.Errorf("attente : %s", body)
	}
	close(lent)
	time.Sleep(100 * time.Millisecond)
	if rec := avecCookie(t, h, loc, cookie); rec.Header().Get("Location") != "/" {
		t.Errorf("fin de préparation : HTTP %d vers %q", rec.Code, rec.Header().Get("Location"))
	}
	body = avecCookie(t, h, "/", cookie).Body.String()
	if !strings.Contains(body, "Démonstration : données fictives.") || !strings.Contains(body, `action="/demo/quitter"`) {
		t.Errorf("démonstration : %s", body)
	}
	// Sans le cookie, l'instance reste a installer.
	if rec := get(t, h, "", "/"); rec.Header().Get("Location") != "/installation" {
		t.Errorf("sans cookie : %q", rec.Header().Get("Location"))
	}

	// Quitter arrete la demonstration et mene au mot de passe.
	rec = posteAvec(t, h, "", "/demo/quitter", url.Values{})
	if rec.Header().Get("Location") != "/installation?etape=mot-de-passe" || arrets != 1 {
		t.Errorf("quitter : %q, %d arrêts", rec.Header().Get("Location"), arrets)
	}
	if rec := avecCookie(t, h, "/", cookie); rec.Header().Get("Location") != "/installation" {
		t.Errorf("après la démonstration : %q", rec.Header().Get("Location"))
	}
}

func avecCookie(t *testing.T, h http.Handler, target string, c *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
