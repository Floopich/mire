package webview

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/floopich/mire/internal/settings"
)

func lire(t *testing.T, h http.Handler, url, auth string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	h.ServeHTTP(rec, req)
	return rec
}

func TestSanteEtatDeLaLigne(t *testing.T) {
	champs := func(rec *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("JSON invalide : %v\n%s", err, rec.Body.String())
		}
		return out
	}

	// Par defaut : rien de la ligne.
	h, _ := serveurAvec(t, 3, essai{Collecting: true})
	if b := champs(lire(t, h, "/sante", "")); b["ligne"] != nil || b["collecte"] != "a_jour" {
		t.Errorf("par défaut : %v", b)
	}

	// Publie sans jeton.
	h, _ = serveurAvec(t, 3, essai{Collecting: true, Forced: settings.Source{settings.SupervisionOn: "true"}})
	b := champs(lire(t, h, "/sante", ""))
	if _, ok := b["ligne"].(string); !ok || b["canaux_a_surveiller"] == nil || b["statut"] != "ok" {
		t.Errorf("publié : %v", b)
	}

	// Avec jeton : /sante reste ouvert, l'etat ne vient qu'avec le jeton.
	h, _ = serveurAvec(t, 3, essai{Collecting: true,
		Forced: settings.Source{settings.SupervisionOn: "true", settings.SupervisionToken: "jeton-secret"}})
	for auth, voit := range map[string]bool{"": false, "Bearer faux": false, "Bearer jeton-secret": true} {
		rec := lire(t, h, "/sante", auth)
		if rec.Code != http.StatusOK {
			t.Errorf("%q : HTTP %d", auth, rec.Code)
		}
		if _, ok := champs(rec)["ligne"]; ok != voit {
			t.Errorf("%q : état visible %v, attendu %v", auth, ok, voit)
		}
	}
}

func TestPointPRTG(t *testing.T) {
	h, _ := serveurAvec(t, 3, essai{Collecting: true})
	if rec := lire(t, h, "/sante/prtg", ""); rec.Code != http.StatusNotFound {
		t.Errorf("désactivé : HTTP %d", rec.Code)
	}

	h, _ = serveurAvec(t, 3, essai{Collecting: true,
		Forced: settings.Source{settings.SupervisionOn: "true", settings.SupervisionToken: "jeton-secret"}})
	if rec := lire(t, h, "/sante/prtg", "Bearer faux"); rec.Code != http.StatusUnauthorized {
		t.Errorf("mauvais jeton : HTTP %d", rec.Code)
	}
	rec := lire(t, h, "/sante/prtg", "Bearer jeton-secret")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("HTTP %d, %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var out struct {
		PRTG struct {
			Result []map[string]any `json:"result"`
			Text   string           `json:"text"`
			Error  int              `json:"error"`
		} `json:"prtg"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	noms := map[string]map[string]any{}
	for _, c := range out.PRTG.Result {
		noms[c["channel"].(string)] = c
	}
	for _, n := range []string{"Collecte", "Ligne", "Canaux à surveiller", "Âge du dernier relevé"} {
		if noms[n] == nil {
			t.Errorf("canal %q absent : %s", n, rec.Body.String())
		}
	}
	if c := noms["Collecte"]; c == nil || c["value"] != 1.0 || c["limitminerror"] != 1.0 {
		t.Errorf("canal Collecte : %v", c)
	}
	if c := noms["Ligne"]; c == nil || c["limitmaxwarning"] != 2.0 || c["limitmaxerror"] != 3.0 || c["limitmode"] != 1.0 {
		t.Errorf("seuils du canal Ligne : %v", c)
	}
	if out.PRTG.Error != 0 || !strings.HasPrefix(out.PRTG.Text, "Ligne : ") {
		t.Errorf("texte : %q, erreur %d", out.PRTG.Text, out.PRTG.Error)
	}
}

func TestRubriqueSupervision(t *testing.T) {
	db := baseRemplie(t, 3)
	h, _ := monter(t, db, essai{Collecting: true,
		Forced: settings.Source{settings.SupervisionOn: "true", settings.SupervisionToken: "jeton-secret"}})
	rec := get(t, h, session(t, db), "/reglages/supervision")
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"http://example.com/sante/prtg", `$string(ligne != "critique")`,
		"Bearer &lt;jeton&gt;", "[BODY].ligne != critique", "Adresse du signal de vie"} {
		if !strings.Contains(body, want) {
			t.Errorf("absent de la rubrique : %q", want)
		}
	}
	if strings.Contains(body, "jeton-secret") {
		t.Error("le jeton apparaît dans la page")
	}
}
