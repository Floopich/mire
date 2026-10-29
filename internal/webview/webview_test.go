package webview

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/store"
)

func baseRemplie(t *testing.T, n int) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatalf("ouverture: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	debut := time.Now().Add(-time.Duration(n) * time.Minute)
	for i := range n {
		raw := docsis.RawSnapshot{
			DOCSIS: "3.1",
			Downstream: []docsis.RawDownstream{
				{ChannelID: 1, Modulation: "256QAM", Frequency: "650 MHz",
					PowerLevel: "1.5", MSE: "-38.9"},
				{ChannelID: 33, Modulation: "OFDM", Frequency: "800 MHz",
					PowerLevel: "2.1", MSE: ""},
			},
			Upstream: []docsis.RawUpstream{{ChannelID: 5, PowerLevel: "45.5"}},
		}
		at := debut.Add(time.Duration(i) * time.Minute)
		if _, err := store.SaveSnapshot(context.Background(), db, raw.Normalize(at)); err != nil {
			t.Fatalf("releve %d: %v", i, err)
		}
	}
	return db
}

func get(t *testing.T, h http.Handler, token, url string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	}
	h.ServeHTTP(rec, req)
	return rec
}

func TestPageIndexListeLesCanaux(t *testing.T) {
	rec := getIdx(t, 10, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d", rec.Code)
	}
	body := rec.Body.String()
	for _, attendu := range []string{"Canal", "256QAM", "OFDM", "/descendant/1", "/descendant/33", "/montant/5"} {
		if !strings.Contains(body, attendu) {
			t.Errorf("la page ne contient pas %q", attendu)
		}
	}
}

func TestPageCanalTraceSesGraphiques(t *testing.T) {
	// Voie descendante : puissance, SNR et erreurs.
	rec := getIdx(t, 30, "/descendant/1")
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d", rec.Code)
	}
	body := rec.Body.String()
	if n := strings.Count(body, `class="chart wide"`); n != 3 {
		t.Errorf("%d graphiques, attendu 3", n)
	}
	if !strings.Contains(body, `class="line"`) || strings.Count(body, `class="chart narrow"`) != 3 {
		t.Error("aucune courbe tracee")
	}
	// Voie montante : puissance seulement.
	if n := strings.Count(getIdx(t, 30, "/montant/5").Body.String(), `class="chart wide"`); n != 1 {
		t.Errorf("voie montante : %d graphiques, attendu 1", n)
	}
}

func TestAucunJavaScriptNiRessourceExterne(t *testing.T) {
	// La page doit rester lisible sur un reseau sans acces exterieur, et ne
	// rien charger que le binaire n'embarque.
	body := getIdx(t, 10, "/descendant/1").Body.String() + getIdx(t, 10, "/").Body.String()
	for _, interdit := range []string{"<script", "http://", "https://", "src="} {
		if strings.Contains(body, interdit) {
			t.Errorf("la page contient %q", interdit)
		}
	}
}

func TestValeurAbsenteAfficheeCommeAbsente(t *testing.T) {
	// Le canal OFDM n'a pas de SNR : il doit apparaitre comme inconnu, pas
	// comme un canal a 0 dB, qui se lirait comme une panne.
	body := getIdx(t, 5, "/").Body.String()
	if !strings.Contains(body, "—") {
		t.Error("aucune valeur absente signalee")
	}
	if strings.Contains(body, "0,0\u00a0dB<") || strings.Contains(body, "0.0 dB<") {
		t.Error("une valeur absente est rendue comme 0.0 dB")
	}
}

func TestBaseVideNeCassePas(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "vide.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	rec := func() *httptest.ResponseRecorder { h, tk := serveur(t, db); return get(t, h, tk, "/") }()
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d sur base vide", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "En attente du premier relevé") || strings.Contains(body, `id="canaux"`) {
		t.Error("sans relevé, la page le dit, sans tableau des canaux vide")
	}
}

func TestFenetreAberranteEstBornee(t *testing.T) {
	// Sans borne, dix ans de releves seraient balayes pour dessiner sept
	// cents pixels.
	for _, url := range []string{"/?heures=-5", "/?heures=999999", "/?heures=abc"} {
		if rec := getIdx(t, 3, url); rec.Code != http.StatusOK {
			t.Errorf("%s: HTTP %d", url, rec.Code)
		}
	}
}

func TestCanalInvalideEstRefuse(t *testing.T) {
	if rec := getIdx(t, 3, "/descendant/abc"); rec.Code != http.StatusBadRequest {
		t.Errorf("HTTP %d, attendu 400", rec.Code)
	}
}

func TestTrouDeMesureInterrompLeTrace(t *testing.T) {
	// Relier deux points de part et d'autre d'un trou ferait croire a une
	// continuite qui n'a pas ete observee.
	maintenant := time.Now()
	points := []store.Point{
		{Timestamp: maintenant, Power: ptr(1)},
		{Timestamp: maintenant.Add(time.Minute), Power: nil},
		{Timestamp: maintenant.Add(2 * time.Minute), Power: ptr(3)},
	}
	svg := SVG(Series{Label: "test", Unit: "dBmV",
		Points: Samples(points, func(p store.Point) *float64 { return p.Power })})
	if strings.Count(svg, "M") < 2 {
		t.Errorf("le trace devrait etre interrompu, obtenu: %s", svg)
	}
}

func TestLigneStableNEstPasRenduePlate(t *testing.T) {
	maintenant := time.Now()
	var points []store.Point
	for i := range 5 {
		points = append(points, store.Point{
			Timestamp: maintenant.Add(time.Duration(i) * time.Minute), Power: ptr(1.5)})
	}
	svg := SVG(Series{Label: "stable", Unit: "dBmV",
		Points: Samples(points, func(p store.Point) *float64 { return p.Power })})
	if strings.Contains(svg, "NaN") || strings.Contains(svg, "Inf") {
		t.Errorf("echelle degeneree sur une ligne stable: %s", svg)
	}
}

func ptr(v float64) *float64 { return &v }

func TestLeVerdictEtLaVoieMontanteSontAffiches(t *testing.T) {
	body := getIdx(t, 5, "/").Body.String()
	if !strings.Contains(body, `class="hero`) {
		t.Error("le verdict n'apparait pas")
	}
	if !strings.Contains(body, "Voie montante") {
		t.Error("les canaux montants n'apparaissent pas")
	}
}

// getIdx ouvre une base peuplee, monte le serveur et interroge une page.
func getIdx(t *testing.T, releves int, url string) *httptest.ResponseRecorder {
	t.Helper()
	h, token := serveur(t, baseRemplie(t, releves))
	return get(t, h, token, url)
}

func TestSansSessionToutRedirigeVersLaConnexion(t *testing.T) {
	h, _ := serveur(t, baseRemplie(t, 3))
	for _, url := range []string{"/", "/descendant/1", "/montant/5"} {
		rec := get(t, h, "", url)
		if rec.Code != http.StatusSeeOther {
			t.Errorf("%s: HTTP %d, attendu une redirection", url, rec.Code)
		}
		if loc := rec.Header().Get("Location"); !strings.Contains(loc, "connexion") {
			t.Errorf("%s: redirige vers %q", url, loc)
		}
	}
}

func TestLaPageDeConnexionResteAccessible(t *testing.T) {
	h, _ := serveur(t, baseRemplie(t, 3))
	rec := get(t, h, "", "/connexion")
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Mot de passe") {
		t.Error("le formulaire n'apparait pas")
	}
}

func TestUnJetonInventeNeDonnePasAcces(t *testing.T) {
	h, _ := serveur(t, baseRemplie(t, 3))
	if rec := get(t, h, "jeton-invente", "/"); rec.Code != http.StatusSeeOther {
		t.Errorf("HTTP %d, un jeton invente devrait etre refuse", rec.Code)
	}
}

func TestConnexionAvecLeBonMotDePasse(t *testing.T) {
	db := baseRemplie(t, 3)
	h, _ := monter(t, db, essai{Collecting: true})

	rec := poste(t, h, "/connexion", "mot_de_passe="+motDePasseTest)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("HTTP %d, attendu une redirection apres connexion", rec.Code)
	}
	var jeton string
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			jeton = c.Value
			if !c.HttpOnly {
				t.Error("le cookie de session doit etre HttpOnly")
			}
			if c.SameSite != http.SameSiteStrictMode {
				t.Error("le cookie de session doit etre SameSite=Strict")
			}
		}
	}
	if jeton == "" {
		t.Fatal("aucun cookie de session")
	}
	if rec := get(t, h, jeton, "/"); rec.Code != http.StatusOK {
		t.Errorf("HTTP %d avec une session valide", rec.Code)
	}
}

func TestMauvaisMotDePasseRefuseSansDirePourquoi(t *testing.T) {
	db := baseRemplie(t, 3)
	h, _ := monter(t, db, essai{Collecting: true})

	rec := poste(t, h, "/connexion", "mot_de_passe=faux")
	body := rec.Body.String()
	if !strings.Contains(body, "incorrect") {
		t.Error("aucun message d'erreur")
	}
	// Le message ne doit pas renseigner un curieux sur l'etat de la config.
	for _, fuite := range []string{"configur", "aucun mot de passe", "empreinte"} {
		if strings.Contains(strings.ToLower(body), fuite) {
			t.Errorf("le message en dit trop : %q", fuite)
		}
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			t.Error("une session est ouverte malgre l'echec")
		}
	}
}

func poste(t *testing.T, h http.Handler, url, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(rec, req)
	return rec
}

func TestSansReleveLeProfilCalibreNEstPasDitNonCalibre(t *testing.T) {
	// Un profil calibre sans releve doit annoncer l'attente, pas une absence
	// de calibration : le message initial induisait en erreur au premier
	// demarrage.
	db, err := store.Open(filepath.Join(t.TempDir(), "vide.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	h, token := serveur(t, db)
	body := get(t, h, token, "/").Body.String()
	if strings.Contains(body, "non calibré") {
		t.Error("un profil calibre est annonce comme non calibre")
	}
	if !strings.Contains(body, "attente du premier relevé") {
		t.Error("l'attente du premier releve n'est pas annoncee")
	}
}

func TestCanauxSeparesParFamille(t *testing.T) {
	// SC-QAM et OFDM ne se lisent pas de la meme facon : chaque voie les
	// presente en deux listes, SC-QAM d'abord.
	body := getIdx(t, 3, "/").Body.String()
	sc := strings.Index(body, `<h4 class="list-sub label-l">SC-QAM <span class="muted">1 canal</span></h4>`)
	ofdm := strings.Index(body, `<h4 class="list-sub label-l">OFDM <span class="muted">1 canal</span></h4>`)
	if sc < 0 || ofdm < sc {
		t.Errorf("listes SC-QAM puis OFDM attendues (positions %d, %d)", sc, ofdm)
	}
	if !strings.Contains(body, "2 canaux") {
		t.Error("le décompte de la voie descendante n'accorde pas « canaux »")
	}
}
