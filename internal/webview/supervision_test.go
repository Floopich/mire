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
		Forced: settings.Source{settings.SupervisionOn: "true", settings.AccessToken: "jeton-secret"}})
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
		Forced: settings.Source{settings.SupervisionOn: "true", settings.AccessToken: "jeton-secret"}})
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
	h, _ := monter(t, db, essai{Collecting: true, Forced: settings.Source{
		settings.SupervisionOn: "true", settings.MetricsOn: "true", settings.AccessToken: "jeton-secret"}})
	tok := session(t, db)
	rec := get(t, h, tok, "/reglages/supervision")
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{">Publier<", ">Signal de vie<", "Métriques Prometheus", `id="r-outil"`,
		"http://example.com/sante/prtg", `$string(collecte = &#34;a_jour&#34; and ligne != &#34;critique&#34;)`,
		"Bearer &lt;jeton&gt;", "[BODY].ligne != critique", ">example.com<"} {
		if !strings.Contains(body, want) {
			t.Errorf("absent de la rubrique : %q", want)
		}
	}
	if strings.Contains(body, "jeton-secret") {
		t.Error("le jeton apparaît dans la page")
	}
	// Une fiche par outil, chacune affichable par la feuille de style.
	css := get(t, h, "", "/static/mire.css").Body.String()
	for _, tool := range recipes("http://m:1340", settings.Fixed(nil), 0) {
		if !strings.Contains(body, `data-outil="`+tool.ID+`"`) || !strings.Contains(css, `.recipe[data-outil="`+tool.ID+`"]`) {
			t.Errorf("fiche %s : absente de la page ou de la feuille de style", tool.ID)
		}
	}
	// L'ancienne rubrique Metriques mene a Supervision.
	if rec := get(t, h, tok, "/reglages/metriques"); rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/reglages/supervision" {
		t.Errorf("ancienne rubrique : HTTP %d vers %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestFichesSuiventLaPublication(t *testing.T) {
	byID := func(v settings.Values) map[string]toolRecipe {
		out := map[string]toolRecipe{}
		for _, r := range recipes("http://m:1340", v, 0) {
			out[r.ID] = r
		}
		return out
	}
	off := byID(settings.Fixed(nil))
	if off["prtg"].Note == "" || off["prom"].Note == "" || len(off["prtg"].Rows) != 0 {
		t.Errorf("rien de publié : PRTG et Prometheus doivent renvoyer à l'interrupteur : %+v", off)
	}
	for _, r := range off["kuma"].Rows {
		if strings.Contains(r.Value, "ligne") || strings.Contains(r.Value, "Bearer") {
			t.Errorf("Uptime Kuma sans état publié : %q", r.Value)
		}
	}
	on := byID(settings.Fixed(map[string]string{settings.SupervisionOn: "true", settings.MetricsOn: "true"}))
	if on["prtg"].Note != "" || on["prom"].Rows[0].Value != "m:1340" {
		t.Errorf("tout publié : %+v %+v", on["prtg"], on["prom"])
	}
}
