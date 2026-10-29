package webview

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/store"
)

func TestEssaiDeSpeedtestTracker(t *testing.T) {
	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer bon-jeton" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprintf(w, `{"data":[{"download":118750000,"upload":6000000,"ping":11,"status":"completed",
			"data":{"timestamp":%q}}],"meta":{"current_page":1,"last_page":1}}`, at.Format(time.RFC3339))
	}))
	defer tracker.Close()

	db := baseRemplie(t, 1)
	h, _ := monter(t, db, essai{})
	token := session(t, db)

	// L'essai part avant d'activer l'option : on verifie d'abord.
	rec := posteAvec(t, h, token, "/reglages/debit/tester", url.Values{
		"debit.adresse": {tracker.URL}, "debit.jeton": {"faux"}})
	if !strings.Contains(rec.Body.String(), "L&#39;essai a échoué : Speedtest Tracker refuse le jeton d&#39;API.") {
		t.Errorf("jeton refusé : %s", rec.Body.String())
	}
	rec = posteAvec(t, h, token, "/reglages/debit/tester", url.Values{
		"debit.adresse": {tracker.URL}, "debit.jeton": {"bon-jeton"}})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Speedtest Tracker répond : 1 mesure lue, la dernière le ") {
		t.Fatalf("HTTP %d : %s", rec.Code, rec.Body.String())
	}

	// La page Internet montre la mesure, le rapport la resume.
	body := get(t, h, token, "/internet?h=24").Body.String()
	for _, voulu := range []string{"Débit", "Descendant médian", "950" + nbsp + "Mbit/s", "48,0" + nbsp + "Mbit/s"} {
		if !strings.Contains(body, voulu) {
			t.Errorf("page Internet : %q absent", voulu)
		}
	}
	if n, _ := store.SpeedTests(context.Background(), db, at.Add(-time.Minute), at.Add(time.Minute)); len(n) != 1 {
		t.Errorf("%d mesures en base", len(n))
	}
}

func TestSansDebitLaSectionResteAbsente(t *testing.T) {
	db := baseRemplie(t, 1)
	h, _ := monter(t, db, essai{})
	if body := get(t, h, session(t, db), "/internet").Body.String(); strings.Contains(body, "Descendant médian") ||
		strings.Contains(body, `id="debit"`) {
		t.Error("section Débit affichée sans Speedtest Tracker")
	}
}

func TestMesureDeDebitAncienne(t *testing.T) {
	at := time.Now().AddDate(0, 0, -25).UTC().Truncate(time.Second)
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"data":[{"download":118750000,"upload":6000000,"status":"completed",
			"data":{"timestamp":%q}}],"meta":{"current_page":1,"last_page":1}}`, at.Format(time.RFC3339))
	}))
	defer tracker.Close()
	db := baseRemplie(t, 1)
	h, _ := monter(t, db, essai{})
	token := session(t, db)
	rec := posteAvec(t, h, token, "/reglages/debit/tester", url.Values{"debit.adresse": {tracker.URL}, "debit.jeton": {"j"}})
	if !strings.Contains(rec.Body.String(), "répond, mais sa dernière mesure date du "+FmtTime(at)) {
		t.Errorf("mesure ancienne : %s", rec.Body.String())
	}
	if body := get(t, h, token, "/internet").Body.String(); !strings.Contains(body, "Aucune mesure de débit sur cette période ; la dernière date du "+FmtTime(at)) {
		t.Error("la dernière mesure connue n'est pas indiquée")
	}
}
