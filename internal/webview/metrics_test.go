package webview

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/floopich/mire/internal/metrics"
	"github.com/floopich/mire/internal/settings"
)

func TestPointMetrics(t *testing.T) {
	scrape := func(h http.Handler, auth string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		h.ServeHTTP(rec, req)
		return rec
	}

	// Desactive par defaut : le point n'existe pas.
	h, _ := serveurAvec(t, 3, essai{Collecting: true})
	if rec := scrape(h, ""); rec.Code != http.StatusNotFound {
		t.Errorf("désactivé : HTTP %d", rec.Code)
	}

	// Active sans jeton : lisible sans session.
	h, _ = serveurAvec(t, 3, essai{Collecting: true, Forced: settings.Source{settings.MetricsOn: "true"}})
	rec := scrape(h, "")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != metrics.ContentType ||
		!strings.Contains(rec.Body.String(), `mire_downstream_power_dbmv{channel="1",modulation="256QAM"} 1.5`) {
		t.Fatalf("sans jeton : HTTP %d, %q\n%s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}

	// Avec jeton : exige en Bearer.
	h, _ = serveurAvec(t, 3, essai{Collecting: true,
		Forced: settings.Source{settings.MetricsOn: "true", settings.MetricsToken: "jeton-secret"}})
	for auth, code := range map[string]int{"": 401, "Bearer faux": 401, "Basic jeton-secret": 401, "Bearer jeton-secret": 200} {
		if rec := scrape(h, auth); rec.Code != code {
			t.Errorf("Authorization %q : HTTP %d, attendu %d", auth, rec.Code, code)
		}
	}
}
