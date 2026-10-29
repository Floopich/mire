package webview

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/auth"
	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/modem"
	"github.com/floopich/mire/internal/settings"
	"github.com/floopich/mire/internal/store"
)

// posteAvec envoie un formulaire, avec une session si token est fourni.
func posteAvec(t *testing.T, h http.Handler, token, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if token != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	}
	h.ServeHTTP(rec, req)
	return rec
}

func cookieDeSession(rec *httptest.ResponseRecorder) string {
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			return c.Value
		}
	}
	return ""
}

func TestPremierDemarrage(t *testing.T) {
	db := baseRemplie(t, 0)
	h, _ := monter(t, db, essai{SansMotDePasse: true})

	// Sans mot de passe, tout mene a l'assistant, y compris la connexion.
	for _, page := range []string{"/", "/reglages", "/connexion"} {
		if rec := get(t, h, "", page); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/installation" {
			t.Errorf("%s : HTTP %d vers %q, attendu l'assistant", page, rec.Code, rec.Header().Get("Location"))
		}
	}
	if rec := get(t, h, "", "/installation"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `name="nouveau"`) {
		t.Fatalf("premiere etape : HTTP %d %s", rec.Code, rec.Body.String())
	}

	court := posteAvec(t, h, "", "/installation", url.Values{"nouveau": {"court"}, "confirmation": {"court"}})
	if court.Code != http.StatusUnprocessableEntity || !strings.Contains(court.Body.String(), "trop court") {
		t.Errorf("mot de passe trop court : HTTP %d", court.Code)
	}
	differents := posteAvec(t, h, "", "/installation", url.Values{"nouveau": {"premier-choix"}, "confirmation": {"autre-chose"}})
	if differents.Code != http.StatusUnprocessableEntity {
		t.Errorf("saisies differentes : HTTP %d", differents.Code)
	}

	rec := posteAvec(t, h, "", "/installation", url.Values{"nouveau": {"premier-choix"}, "confirmation": {"premier-choix"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/installation/operateur" {
		t.Fatalf("choix du mot de passe : HTTP %d vers %q", rec.Code, rec.Header().Get("Location"))
	}
	token := cookieDeSession(rec)
	if token == "" {
		t.Fatal("l'assistant doit ouvrir une session")
	}
	if rec := get(t, h, token, "/installation/operateur"); rec.Code != http.StatusOK {
		t.Errorf("etape operateur : HTTP %d", rec.Code)
	}

	// L'etape est desormais fermee : personne d'autre sur le reseau ne peut
	// remplacer le mot de passe.
	rec = posteAvec(t, h, "", "/installation", url.Values{"nouveau": {"intrus-intrus"}, "confirmation": {"intrus-intrus"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/connexion" {
		t.Errorf("seconde visite : HTTP %d vers %q", rec.Code, rec.Header().Get("Location"))
	}
	if ok, _ := auth.Check(context.Background(), db, "premier-choix"); !ok {
		t.Error("le mot de passe choisi a ete remplace")
	}
	if rec := get(t, h, "", "/installation"); rec.Header().Get("Location") != "/connexion" {
		t.Errorf("l'assistant reste ouvert apres le choix : %q", rec.Header().Get("Location"))
	}
}

func TestLAssistantEnregistreLOperateur(t *testing.T) {
	db := baseRemplie(t, 0)
	h, svc := monter(t, db, essai{})
	token := session(t, db)
	body := get(t, h, token, "/installation/operateur").Body.String()
	if strings.Index(body, "Telenet / Orange") < 0 || strings.Index(body, "VOO / Orange") > strings.Index(body, "Opérateur non calibré") {
		t.Errorf("profils proposés, calibrés d'abord : %s", body)
	}
	rec := posteAvec(t, h, token, "/installation/operateur", url.Values{"profil": {"generique"}, "intervalle": {"20m"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/installation/modem" {
		t.Fatalf("HTTP %d vers %q", rec.Code, rec.Header().Get("Location"))
	}
	if st := svc.State(); st.Profile.ID != "generique" || st.Interval != 20*time.Minute || svc.reloads != 1 {
		t.Errorf("profil %s, intervalle %v, %d rechargements", st.Profile.ID, st.Interval, svc.reloads)
	}
}

func TestPageReglages(t *testing.T) {
	db := baseRemplie(t, 1)
	h, svc := monter(t, db, essai{})
	token := session(t, db)

	body := get(t, h, token, "/reglages").Body.String()
	// La liste des rubriques, chacune avec son resume, et la premiere.
	for _, attendu := range []string{`href="/reglages/modem"`, `href="/reglages/collecte"`, `href="/reglages/analyse"`,
		`href="/reglages/sonde"`, `href="/reglages/notifications"`, `href="/reglages/abonnement"`,
		`href="/reglages/securite"`, `href="/reglages/sauvegarde"`, "Mot de passe à renseigner : collecte arrêtée",
		`name="modem.adresse"`} {
		if !strings.Contains(body, attendu) {
			t.Errorf("page Reglages sans %s", attendu)
		}
	}
	for _, rubrique := range []string{"modem", "collecte", "analyse", "sonde", "notifications", "abonnement", "securite", "sauvegarde"} {
		if rec := get(t, h, token, "/reglages/"+rubrique); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `aria-current="page"`) {
			t.Errorf("rubrique %s : HTTP %d", rubrique, rec.Code)
		}
	}
	if rec := get(t, h, token, "/reglages/inconnue"); rec.Code != http.StatusNotFound {
		t.Errorf("rubrique inconnue : HTTP %d", rec.Code)
	}

	rec := posteAvec(t, h, token, "/reglages/collecte", url.Values{"collecte.intervalle": {"15m"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/reglages/collecte?enregistre=1" {
		t.Fatalf("enregistrement : HTTP %d vers %q", rec.Code, rec.Header().Get("Location"))
	}
	if st := svc.State(); st.Interval != 15*time.Minute || svc.reloads != 1 {
		t.Errorf("intervalle %v apres %d rechargements", st.Interval, svc.reloads)
	}
	if body := get(t, h, token, "/reglages/collecte?enregistre=1").Body.String(); !strings.Contains(body, "Réglages enregistrés") ||
		!strings.Contains(body, "Toutes les 15"+nbsp+"min") {
		t.Error("l'enregistrement n'est pas confirme, ou le resume n'est pas a jour")
	}

	posteAvec(t, h, token, "/reglages/modem", url.Values{"modem.mot_de_passe": {"secret"}, "modem.identifiant": {"admin"}})
	// Le mot de passe enregistre n'est jamais renvoye dans la page.
	body = get(t, h, token, "/reglages/modem?enregistre=1").Body.String()
	if strings.Contains(body, "secret") {
		t.Error("le mot de passe du modem apparait dans la page")
	}
	if !strings.Contains(body, "(enregistré)") {
		t.Error("la page ne dit pas qu'un mot de passe est enregistre")
	}
}

func TestUneCaseDecocheeEstEnregistree(t *testing.T) {
	db := baseRemplie(t, 1)
	h, svc := monter(t, db, essai{})
	token := session(t, db)
	// Actif par defaut ; le navigateur n'envoie pas une case decochee.
	posteAvec(t, h, token, "/reglages/collecte", url.Values{"collecte.intervalle": {"10m"}})
	if svc.State().Values.Bool(settings.Burst) {
		t.Fatal("case decochee non enregistree")
	}
	posteAvec(t, h, token, "/reglages/collecte", url.Values{"collecte.intervalle": {"10m"}, "collecte.rapprochee": {"true"}})
	if !svc.State().Values.Bool(settings.Burst) {
		t.Error("case cochee non enregistree")
	}
}

func TestModulationOFDMAHabituelle(t *testing.T) {
	db := baseRemplie(t, 1)
	// Trois releves OFDMA en 16QAM, un en 32QAM : l'aide le rapporte.
	for i, m := range []string{"16QAM", "16QAM", "16QAM", "32QAM"} {
		raw := docsis.RawSnapshot{DOCSIS: "3.1", Upstream: []docsis.RawUpstream{{ChannelID: 9, Modulation: "OFDMA", Multiplex: m, PowerLevel: "41"}}}
		if _, err := store.SaveSnapshot(t.Context(), db, raw.Normalize(time.Now().Add(-time.Duration(i)*time.Hour))); err != nil {
			t.Fatal(err)
		}
	}
	h, svc := monter(t, db, essai{})
	token := session(t, db)
	body := get(t, h, token, "/reglages/analyse").Body.String()
	if !strings.Contains(body, "Relevé sur 30 jours : 16QAM (75\u202f%), 32QAM (25\u202f%).") {
		t.Errorf("la modulation observee n'est pas rapportee")
	}
	rec := posteAvec(t, h, token, "/reglages/analyse", url.Values{"operateur.profil": {"voo"}, "analyse.ofdma_plancher": {"16"}})
	if rec.Code != http.StatusSeeOther || svc.State().Values.Int(settings.OFDMAFloor) != 16 {
		t.Fatalf("HTTP %d, plancher %d", rec.Code, svc.State().Values.Int(settings.OFDMAFloor))
	}
	if body := get(t, h, token, "/reglages").Body.String(); !strings.Contains(body, "OFDMA habituel 16QAM") {
		t.Error("le resume de l'analyse ne mentionne pas la modulation habituelle")
	}
	if rec := posteAvec(t, h, token, "/reglages/analyse", url.Values{"analyse.ofdma_plancher": {"12"}}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("modulation inconnue : HTTP %d", rec.Code)
	}
}

func TestLAbonnementNeConcerneQueLaBelgique(t *testing.T) {
	db := baseRemplie(t, 1)
	h, _ := monter(t, db, essai{Forced: settings.Source{settings.Profile: "generique"}})
	token := session(t, db)
	if strings.Contains(get(t, h, token, "/reglages").Body.String(), `href="/reglages/abonnement"`) {
		t.Error("la rubrique Abonnement ne concerne qu'un operateur belge")
	}
	if rec := get(t, h, token, "/reglages/abonnement"); rec.Code != http.StatusNotFound {
		t.Errorf("HTTP %d", rec.Code)
	}
}

func TestUneSaisieInvalideEstExpliquee(t *testing.T) {
	db := baseRemplie(t, 1)
	h, svc := monter(t, db, essai{})
	token := session(t, db)
	rec := posteAvec(t, h, token, "/reglages/collecte", url.Values{"collecte.intervalle": {"10s"}, "conservation.jours": {"90"}})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "une minute au minimum") {
		t.Errorf("HTTP %d, message absent", rec.Code)
	}
	// La saisie est rendue telle quelle, pour etre corrigee.
	if !strings.Contains(rec.Body.String(), `value="10s"`) {
		t.Error("la saisie refusee n'est pas conservee")
	}
	if svc.reloads != 0 {
		t.Error("une saisie refusee ne doit rien relancer")
	}
	if v, _ := store.Setting(context.Background(), db, "reglage.modem.identifiant", ""); v != "" {
		t.Error("le groupe a ete enregistre a moitie")
	}
}

func TestUnReglageImposeEstVerrouille(t *testing.T) {
	db := baseRemplie(t, 1)
	h, svc := monter(t, db, essai{Forced: settings.Source{settings.Interval: "5m"}})
	token := session(t, db)
	body := get(t, h, token, "/reglages/collecte").Body.String()
	if !strings.Contains(body, "MIRE_INTERVALLE") {
		t.Error("le champ impose ne dit pas quelle variable le fixe")
	}
	posteAvec(t, h, token, "/reglages/collecte", url.Values{"collecte.intervalle": {"30m"}})
	if st := svc.State(); st.Interval != 5*time.Minute {
		t.Errorf("intervalle %v : la variable doit l'emporter", st.Interval)
	}
}

func TestReglagesProteges(t *testing.T) {
	db := baseRemplie(t, 1)
	h, _ := monter(t, db, essai{})
	token := session(t, db)

	if rec := get(t, h, "", "/reglages"); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/connexion" {
		t.Errorf("sans session : HTTP %d vers %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := posteAvec(t, h, token, "/reglages/inconnu", url.Values{}); rec.Code != http.StatusNotFound {
		t.Errorf("groupe inconnu : HTTP %d", rec.Code)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/reglages/modem", strings.NewReader("collecte.intervalle=1h"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("formulaire d'un autre site : HTTP %d", rec.Code)
	}
}

func TestChangementDuMotDePasse(t *testing.T) {
	db := baseRemplie(t, 1)
	h, _ := monter(t, db, essai{})
	token := session(t, db)

	faux := posteAvec(t, h, token, "/reglages/mot-de-passe", url.Values{
		"actuel": {"faux"}, "nouveau": {"nouveau-secret"}, "confirmation": {"nouveau-secret"}})
	if faux.Code != http.StatusUnprocessableEntity {
		t.Errorf("mot de passe actuel faux : HTTP %d", faux.Code)
	}
	rec := posteAvec(t, h, token, "/reglages/mot-de-passe", url.Values{
		"actuel": {motDePasseTest}, "nouveau": {"nouveau-secret"}, "confirmation": {"nouveau-secret"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("HTTP %d", rec.Code)
	}
	// Les autres sessions sont fermees ; l'appareil qui a fait le changement
	// en recoit une nouvelle.
	if get(t, h, token, "/").Code != http.StatusSeeOther {
		t.Error("l'ancienne session reste ouverte")
	}
	if nouveau := cookieDeSession(rec); nouveau == "" || get(t, h, nouveau, "/").Code != http.StatusOK {
		t.Error("aucune session valide apres le changement")
	}
}

func TestFeuilleDesChampsConditionnels(t *testing.T) {
	h, _ := serveurAvec(t, 1, essai{})
	rec := get(t, h, "", "/reglages.css")
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("HTTP %d, %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Body.String(), "@supports selector(:has(a))") {
		t.Error("les champs conditionnels doivent rester visibles sans :has()")
	}
}

// Les etapes qui dependent du reseau (detection, test du modem) ne sont pas
// joignables en test : leurs gabarits sont rendus directement, pour qu'une
// erreur de gabarit ne se decouvre pas en production.
func TestToutesLesVariantesDesGabaritsSeRendent(t *testing.T) {
	db := baseRemplie(t, 1)
	svc := &fauxService{db: db, forced: settings.Source{settings.ModemModel: "cga4233"}}
	if err := svc.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(db, Options{Service: svc})
	if err != nil {
		t.Fatal(err)
	}
	m, _ := modem.Lookup("cga4233")
	trouves := []modem.Candidate{
		{URL: "http://192.168.100.1", Title: "Technicolor", DOCSIS: true, Model: &m},
		{URL: "http://192.168.0.1", DOCSIS: true},
		{URL: "http://192.168.1.1", Title: "Box"},
	}
	req := httptest.NewRequest(http.MethodGet, "/reglages", nil)

	for _, v := range []setupView{
		s.setupView("mot-de-passe", 1),
		func() setupView {
			v := s.setupView("modem", 3)
			v.Detected = trouves
			v.Address = trouves[0].URL
			return v
		}(),
		func() setupView { v := s.setupView("modem", 3); v.Err = "Adresse : invalide"; return v }(),
		func() setupView {
			v := s.setupView("modem", 3)
			v.Test = &testResult{Message: "Le modem n'a pas répondu."}
			return v
		}(),
		func() setupView {
			v := s.setupView("modem", 3)
			v.Test = &testResult{OK: true, Message: "Le modem répond."}
			return v
		}(),
		s.setupView("operateur", 2),
	} {
		rec := httptest.NewRecorder()
		s.render(rec, http.StatusOK, "setup", v)
		if rec.Code != http.StatusOK {
			t.Errorf("etape %s : HTTP %d", v.Step, rec.Code)
		}
	}

	for _, v := range []reglagesView{
		s.reglagesView(req, "modem", false, nil),
		func() reglagesView { v := s.reglagesView(req, "modem", true, nil); v.Detecting = true; return v }(),
		func() reglagesView {
			v := s.reglagesView(req, "modem", true, nil)
			v.Detecting, v.Detected = true, trouves
			return v
		}(),
		func() reglagesView {
			v := s.reglagesView(req, "modem", true, nil)
			v.Test = &testResult{OK: true, Message: "ok"}
			return v
		}(),
		func() reglagesView {
			v := s.reglagesView(req, "notifications", true, nil)
			v.Test = &testResult{Message: "non"}
			return v
		}(),
		func() reglagesView {
			v := s.reglagesView(req, "securite", true, nil)
			v.PasswordErr, v.PasswordOK = "faux", true
			return v
		}(),
		s.reglagesView(req, "sauvegarde", true, nil),
		s.reglagesView(req, "analyse", true, nil),
	} {
		rec := httptest.NewRecorder()
		s.render(rec, http.StatusOK, "reglages", v)
		if rec.Code != http.StatusOK {
			t.Errorf("page Reglages : HTTP %d", rec.Code)
		}
		if v.Section == "modem" && !strings.Contains(rec.Body.String(), "MIRE_MODELE") {
			t.Error("le modele impose doit etre signale comme verrouille")
		}
	}
}

func TestUneOptionDesactiveeParUneVariableEstFigee(t *testing.T) {
	db := baseRemplie(t, 1)
	h, _ := monter(t, db, essai{Forced: settings.Source{settings.ProbeOn: "false"}})
	body := get(t, h, session(t, db), "/reglages/sonde").Body.String()
	i := strings.Index(body, `class="card settings-panel"`)
	if i < 0 {
		t.Fatal("rubrique de la sonde introuvable")
	}
	groupe := body[i:]
	groupe = groupe[:strings.Index(groupe, "</section>")]
	if !strings.Contains(groupe, "MIRE_SONDE") || strings.Contains(groupe, ">Enregistrer<") {
		t.Errorf("groupe desactive par une variable : %s", groupe)
	}
}

func TestLesChampsDUnServiceNApparaissentQuePourLui(t *testing.T) {
	db := baseRemplie(t, 1)
	h, _ := monter(t, db, essai{})
	token := session(t, db)
	body := get(t, h, token, "/reglages/notifications").Body.String()
	if !strings.Contains(body, `class="field-row only" data-condition="notif.service=ntfy"`) {
		t.Error("les champs ntfy ne portent pas leur condition d'affichage")
	}
	css := get(t, h, "", "/reglages.css").Body.String()
	if !strings.Contains(css, `.only[data-condition="notif.service=ntfy"]`) {
		t.Errorf("la feuille ne cible pas les champs ntfy : %s", css)
	}
}

func TestEssaiDeNotification(t *testing.T) {
	recu := make(chan map[string]any, 1)
	ntfy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		recu <- body
	}))
	defer ntfy.Close()

	db := baseRemplie(t, 1)
	h, _ := monter(t, db, essai{})
	token := session(t, db)

	// L'essai part meme avant d'activer l'option : on verifie d'abord.
	rec := posteAvec(t, h, token, "/reglages/notifications/tester", url.Values{
		"notif.service": {"ntfy"}, "notif.ntfy.serveur": {ntfy.URL}, "notif.ntfy.sujet": {"ma-ligne"},
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Notification d&#39;essai envoyée") {
		t.Fatalf("HTTP %d", rec.Code)
	}
	select {
	case body := <-recu:
		if body["topic"] != "ma-ligne" || body["title"] != "Mire : essai" {
			t.Errorf("essai recu : %v", body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("aucun essai recu")
	}

	rec = posteAvec(t, h, token, "/reglages/notifications/tester", url.Values{"notif.service": {"gotify"}})
	if !strings.Contains(rec.Body.String(), "Essai impossible : serveur Gotify non renseigné.") {
		t.Error("un service incomplet doit etre explique")
	}
}

func TestDesNotificationsIncompletesSontSignalees(t *testing.T) {
	db := baseRemplie(t, 1)
	h, svc := monter(t, db, essai{})
	svc.st.NotifyErr = errors.New("sujet ntfy non renseigné")
	body := get(t, h, session(t, db), "/reglages/notifications").Body.String()
	if !strings.Contains(body, "Les notifications ne partent pas : sujet ntfy non renseigné.") {
		t.Error("la carte Notifications ne dit pas pourquoi rien ne part")
	}
}

func TestLaPremiereVisiteDonneLAdresseDesNotifications(t *testing.T) {
	db := baseRemplie(t, 0)
	h, svc := monter(t, db, essai{SansMotDePasse: true})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/installation", strings.NewReader(url.Values{"nouveau": {"premier-choix"}, "confirmation": {"premier-choix"}}.Encode()))
	req.Host = "nas.local:1340"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "mire.exemple.be")
	h.ServeHTTP(rec, req)
	if err := svc.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := svc.State().Values.String(settings.NotifyLink); got != "https://mire.exemple.be" {
		t.Errorf("adresse %q", got)
	}
}
