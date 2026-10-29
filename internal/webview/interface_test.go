package webview

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/analyze"
)

func TestSanteRepondSansSessionEtSansDetailDeLaLigne(t *testing.T) {
	h, _ := serveurAvec(t, 3, essai{Collecting: true, Version: "v9"})
	rec := get(t, h, "", "/sante")
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("JSON illisible: %v", err)
	}
	if body["statut"] != "ok" || body["collecte"] != "a_jour" || body["version"] != "v9" {
		t.Errorf("reponse inattendue: %v", body)
	}
	for _, fuite := range []string{"dBmV", "QAM", "canal"} {
		if strings.Contains(rec.Body.String(), fuite) {
			t.Errorf("la sonde publie %q", fuite)
		}
	}
}

func TestUneCollecteMuetteEstSignalee(t *testing.T) {
	// Une ligne coupee ne produit plus de releve : c'est l'absence de
	// donnees qui doit alerter, sur la page comme dans la sonde.
	plusTard := func() time.Time { return time.Now().Add(2 * time.Hour) }
	h, token := serveurAvec(t, 3, essai{Collecting: true, Interval: 10 * time.Minute, Now: plusTard})
	if body := get(t, h, token, "/").Body.String(); !strings.Contains(body, "Aucun nouveau relevé depuis") {
		t.Error("la page ne signale pas une collecte muette")
	}
	if body := get(t, h, "", "/sante").Body.String(); !strings.Contains(body, "en_retard") {
		t.Errorf("la sonde ne signale pas le retard: %s", body)
	}
}

func TestUneCollecteInactiveEstExpliquee(t *testing.T) {
	h, token := serveurAvec(t, 3, essai{Collecting: false})
	body := get(t, h, token, "/").Body.String()
	if !strings.Contains(body, "mot de passe du modem") || !strings.Contains(body, `href="/reglages/modem"`) {
		t.Error("la page n'explique pas comment activer la collecte")
	}
}

func TestEnTetesDeSecuriteSurToutesLesReponses(t *testing.T) {
	h, token := serveurAvec(t, 3, essai{Collecting: true})
	for _, url := range []string{"/", "/connexion", "/sante", "/static/mire.css", "/introuvable"} {
		rec := get(t, h, token, url)
		csp := rec.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, "default-src 'none'") || strings.Contains(csp, "unsafe") {
			t.Errorf("%s: CSP %q", url, csp)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: nosniff absent", url)
		}
	}
}

func TestLesRessourcesStatiquesSontServies(t *testing.T) {
	h, _ := serveurAvec(t, 1, essai{})
	for url, typ := range map[string]string{"/static/mire.css": "text/css", "/static/mire.svg": "image/svg+xml"} {
		rec := get(t, h, "", url)
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), typ) {
			t.Errorf("%s: HTTP %d, type %q", url, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
}

func TestPageDUnCanalMontant(t *testing.T) {
	h, token := serveurAvec(t, 10, essai{Collecting: true})
	rec := get(t, h, token, "/montant/5")
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d", rec.Code)
	}
	body := rec.Body.String()
	// La voie montante n'a pas de SNR : une seule courbe, avec la plage VOO.
	if n := strings.Count(body, `class="chart wide"`); n != 1 {
		t.Errorf("%d graphiques, attendu 1", n)
	}
	if !strings.Contains(body, `class="band"`) {
		t.Error("la plage nominale n'est pas dessinee")
	}
}

func TestCanalInconnuEtPageInconnue(t *testing.T) {
	h, token := serveurAvec(t, 3, essai{})
	if rec := get(t, h, token, "/descendant/999"); rec.Code != http.StatusNotFound {
		t.Errorf("canal inconnu : HTTP %d, attendu 404", rec.Code)
	}
	if rec := get(t, h, token, "/nimporte/quoi"); rec.Code != http.StatusNotFound {
		t.Errorf("page inconnue : HTTP %d, attendu 404", rec.Code)
	}
}

func TestLAncienneAdresseDesCanauxRedirige(t *testing.T) {
	h, token := serveurAvec(t, 3, essai{})
	rec := get(t, h, token, "/canal/1?heures=6")
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/descendant/1?heures=6" {
		t.Errorf("HTTP %d vers %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestLaBandeDeCanauxPlaceChaqueCanal(t *testing.T) {
	h, token := serveurAvec(t, 3, essai{Collecting: true})
	body := get(t, h, token, "/").Body.String()
	if !strings.Contains(body, `class="spectrum wide"`) {
		t.Fatal("la bande de canaux n'est pas dessinee")
	}
	for _, lien := range []string{`href="/descendant/1?heures=24"`, `href="/descendant/33?heures=24"`} {
		if !strings.Contains(body, lien) {
			t.Errorf("la bande ne mene pas a %s", lien)
		}
	}
}

func TestUneConnexionDepuisUnAutreSiteEstRefusee(t *testing.T) {
	h, _ := serveurAvec(t, 1, essai{})
	cas := map[string]map[string]string{
		"Sec-Fetch-Site":     {"Sec-Fetch-Site": "cross-site"},
		"Origin etrangere":   {"Origin": "https://attaquant.net"},
		"Origin par suffixe": {"Origin": "http://example.com.attaquant.net"},
	}
	for nom, entetes := range cas {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/connexion", strings.NewReader("mot_de_passe=x"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for k, v := range entetes {
			req.Header.Set(k, v)
		}
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s : HTTP %d, attendu 403", nom, rec.Code)
		}
	}
}

func TestFormatsALaFrancaise(t *testing.T) {
	v := 1.5
	if got := Fmt(&v, "dBmV"); got != "1,5 dBmV" {
		t.Errorf("Fmt = %q", got)
	}
	if got := FmtCount(12480); got != "12 480" {
		t.Errorf("FmtCount = %q", got)
	}
	now := time.Now()
	for d, want := range map[time.Duration]string{
		10 * time.Second: "à l'instant",
		4 * time.Minute:  "il y a 4 min",
		3 * time.Hour:    "il y a 3 h",
		72 * time.Hour:   "il y a 3 jours",
	} {
		if got := Ago(now.Add(-d), now); got != want {
			t.Errorf("Ago(%v) = %q, attendu %q", d, got, want)
		}
	}
	if Label(analyze.Warning) != "À surveiller" {
		t.Error("libelle d'avertissement")
	}
}
