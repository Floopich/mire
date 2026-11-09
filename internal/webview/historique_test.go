package webview

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/store"
)

func TestMenuEtHistorique(t *testing.T) {
	db := baseRemplie(t, 3)
	h, token := serveur(t, db)

	// Cinq entrees nommees ; celle de la page en cours est signalee.
	for page, section := range map[string]string{
		"/": "/", "/descendant/1": "/", "/comprendre": "/",
		"/internet":   "/internet",
		"/historique": "/historique", "/chronologie": "/historique", "/evenements": "/historique",
		"/tendance": "/historique", "/rapport": "/historique",
		"/modem": "/modem", "/reglages": "/reglages", "/reglages/collecte": "/reglages",
	} {
		body := get(t, h, token, page).Body.String()
		for _, nom := range []string{">Ligne<", ">Internet<", ">Historique<", ">Modem<", ">Réglages<"} {
			if !strings.Contains(body, nom) {
				t.Errorf("%s : entrée %s absente du menu", page, nom)
			}
		}
		if !strings.Contains(body, `href="`+section+`" aria-current="page"`) {
			t.Errorf("%s : l'entrée %s n'est pas signalée", page, section)
		}
	}

	// L'historique mene a chaque page qui regarde en arriere.
	body := get(t, h, token, "/historique").Body.String()
	for _, href := range []string{"/chronologie", "/evenements", "/tendance", "/rapport"} {
		if !strings.Contains(body, `href="`+href+`"`) {
			t.Errorf("historique sans lien vers %s", href)
		}
	}
	if !strings.Contains(get(t, h, token, "/tendance").Body.String(), `href="/historique" aria-label="Revenir à l'historique"`) {
		t.Error("la tendance ne ramène pas à l'historique")
	}
}

func TestLesCanauxEnDefautRestentVisibles(t *testing.T) {
	// Ligne saine : la liste se replie sous son resume. Un canal en defaut
	// reste visible au-dessus, sans deplier la liste.
	db := baseRemplie(t, 1)
	h, token := serveur(t, db)
	body := get(t, h, token, "/").Body.String()
	if !strings.Contains(body, `<section class="channels card" id="canaux"`) || strings.Contains(body, ">Voie descendante<") ||
		!strings.Contains(body, "tous en bon état") || !strings.Contains(body, `href="/?canaux=1#canaux"`) {
		t.Errorf("liste saine non repliée ou sans résumé")
	}
	// L'etat deplie tient dans l'adresse : le rechargement automatique de la
	// page ne la replie pas.
	ouverte := get(t, h, token, "/?canaux=1").Body.String()
	if !strings.Contains(ouverte, `class="channels card open"`) || !strings.Contains(ouverte, ">Voie descendante<") {
		t.Error("la liste ne se déplie pas avec ?canaux=1")
	}
	if strings.Contains(body, ">À surveiller</h2>") {
		t.Error("section À surveiller affichée sur une ligne saine")
	}
	if !strings.Contains(get(t, h, token, "/?heures=168").Body.String(), `class="channels card open"`) {
		t.Error("la liste doit rester dépliée après un changement de période")
	}

	snr := 20.0
	snap := docsis.Snapshot{Timestamp: time.Now(), DOCSIS: "3.1", Downstream: []docsis.Downstream{
		{ChannelID: 1, FrequencyMHz: ptr(442), Modulation: "256QAM", PowerDBmV: ptr(1), SNRDB: &snr},
		{ChannelID: 2, FrequencyMHz: ptr(450), Modulation: "256QAM", PowerDBmV: ptr(1), SNRDB: ptr(40)},
	}}
	if _, err := store.SaveSnapshot(context.Background(), db, snap); err != nil {
		t.Fatal(err)
	}
	body = get(t, h, token, "/").Body.String()
	att := strings.Index(body, ">À surveiller</h2>")
	det := strings.Index(body, `id="canaux"`)
	if att < 0 || det < att || !strings.Contains(body[att:det], `href="/descendant/1?`) {
		t.Error("le canal en défaut n'est pas affiché au-dessus de la liste")
	}
	if !strings.Contains(body, "dont 1 à surveiller") {
		t.Error("le résumé ne compte pas le canal en défaut")
	}
}

func TestHistoriqueMeneAuDernierReleve(t *testing.T) {
	// Les releves complets s'ouvrent d'un clic sur une courbe, hors du
	// parcours au clavier : l'historique y mene, a partir du dernier.
	h, token := serveur(t, baseRemplie(t, 3))
	body := get(t, h, token, "/historique").Body.String()
	i := strings.Index(body, `href="/releve?t=`)
	if i < 0 || !strings.Contains(body, "Relevés") {
		t.Fatal("pas d'entrée vers les relevés")
	}
	href := body[i+6 : i+6+strings.Index(body[i+6:], `"`)]
	if rec := get(t, h, token, strings.ReplaceAll(href, "&amp;", "&")); rec.Code != http.StatusOK {
		t.Errorf("%s : HTTP %d", href, rec.Code)
	}
	vide, tokenVide := serveur(t, baseRemplie(t, 0))
	if strings.Contains(get(t, vide, tokenVide, "/historique").Body.String(), "/releve") {
		t.Error("entrée des relevés affichée sans aucun relevé")
	}
}
