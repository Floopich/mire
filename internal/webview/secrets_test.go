package webview

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/journal"
	"github.com/floopich/mire/internal/settings"
)

// Chaque reglage secret recoit une valeur temoin ; aucune page, aucun essai,
// aucune ligne du journal ne doit la laisser paraitre.
func TestAucunSecretNeSortDeLInterface(t *testing.T) {
	defer func(d time.Duration) { quickWait = d }(quickWait)
	quickWait = 3 * time.Second

	db := baseRemplie(t, 3)
	ctx := context.Background()
	temoins := map[string]string{}
	input := map[string]string{
		settings.ModemURL: "http://127.0.0.1:1", settings.MetricsOn: "true",
		settings.MQTTOn: "true", settings.MQTTServer: "mqtt://127.0.0.1:1",
		settings.SpeedOn: "true", settings.SpeedURL: "http://127.0.0.1:1",
		settings.NotifyOn: "true", settings.NotifyService: "discord",
	}
	n := 0
	for _, g := range settings.Groups() {
		for _, d := range g.Defs {
			if d.Kind != settings.Password {
				continue
			}
			n++
			v := fmt.Sprintf("temoin%02dsecret", n)
			if d.Key == settings.DiscordURL || d.Key == settings.SlackURL {
				v = "http://127.0.0.1:1/api/webhooks/" + v
			}
			temoins[d.Key] = fmt.Sprintf("temoin%02dsecret", n)
			input[d.Key] = v
		}
	}
	if err := settings.Set(ctx, db, settings.Source{}, input); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	jr, lg := journal.New(&out)
	prev := slog.Default()
	slog.SetDefault(lg)
	defer slog.SetDefault(prev)
	h, svc := monter(t, db, essai{Journal: jr, Collecting: true})
	if err := svc.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	token := session(t, db)

	verifie := func(ou, texte string) {
		t.Helper()
		for k, v := range temoins {
			if strings.Contains(texte, v) {
				t.Errorf("%s : le secret %s paraît", ou, k)
			}
		}
	}
	pages := []string{"/", "/modem", "/internet", "/evenements", "/rapport", "/comprendre", "/tendance", "/reglages", "/sante",
		"/installation/options"}
	for _, g := range settings.Groups() {
		pages = append(pages, "/reglages/"+g.ID)
	}
	for _, p := range pages {
		verifie(p, get(t, h, token, p).Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+temoins[settings.MetricsToken])
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("/metrics : HTTP %d", rec.Code)
	}
	verifie("/metrics", rec.Body.String())

	for rubrique, form := range map[string]url.Values{
		"notifications": {settings.NotifyOn: {"on"}},
		"domotique":     {settings.MQTTOn: {"on"}},
		"debit":         {settings.SpeedOn: {"on"}},
		"modem":         {},
	} {
		rec := posteAvec(t, h, token, "/reglages/"+rubrique+"/tester", form)
		body := rec.Body.String()
		if rec.Code == http.StatusSeeOther {
			loc := rec.Header().Get("Location")
			for i := 0; i < 50; i++ {
				time.Sleep(200 * time.Millisecond)
				body = get(t, h, token, loc).Body.String()
				if !strings.Contains(body, `http-equiv="refresh"`) {
					break
				}
			}
		}
		if !strings.Contains(body, "notice") {
			t.Errorf("essai %s : pas de résultat", rubrique)
		}
		verifie("essai "+rubrique, body)
	}
	verifie("/reglages/journal/telecharger", get(t, h, token, "/reglages/journal/telecharger").Body.String())
	if !strings.Contains(out.String(), "essai MQTT") {
		t.Error("le journal du service doit avoir reçu les échecs des essais")
	}
	verifie("journal du service", out.String())
}
