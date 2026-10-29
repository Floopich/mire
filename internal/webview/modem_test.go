package webview

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/events"
	"github.com/floopich/mire/internal/modem"
	"github.com/floopich/mire/internal/settings"
	"github.com/floopich/mire/internal/store"
)

// fauxPilote joue un modem gerable.
type fauxPilote struct {
	info         modem.Info
	router       modem.Router
	restartErr   error
	lectures     int
	redemarrages int
	// lent retarde la lecture des informations.
	lent time.Duration
}

func (p *fauxPilote) Snapshot(context.Context) (docsis.RawSnapshot, error) {
	time.Sleep(p.lent)
	return docsis.RawSnapshot{}, nil
}

func (p *fauxPilote) Info(context.Context) (modem.Info, error) {
	time.Sleep(p.lent)
	p.lectures++
	return p.info, nil
}

func (p *fauxPilote) Router(context.Context) (modem.Router, error) { return p.router, nil }

func (p *fauxPilote) Restart(context.Context) error {
	p.redemarrages++
	return p.restartErr
}

// piloteSimple ne sait que relever.
type piloteSimple struct{}

func (piloteSimple) Snapshot(context.Context) (docsis.RawSnapshot, error) {
	return docsis.RawSnapshot{}, nil
}

func modemConfigure() settings.Source {
	return settings.Source{settings.ModemModel: "cga4233", settings.ModemPassword: "x"}
}

func serveurModem(t *testing.T, p modem.Driver) (http.Handler, string, *sql.DB) {
	t.Helper()
	db := baseRemplie(t, 1)
	h, _ := monter(t, db, essai{Collecting: true, Forced: modemConfigure(), Pilote: p})
	return h, session(t, db), db
}

func pilotePont() *fauxPilote {
	return &fauxPilote{info: modem.Info{Model: "CGA4233VOO", Manufacturer: "Technicolor", Software: "1.2.3",
		Status: "OPERATIONAL", Uptime: 50 * time.Hour, Mode: modem.ModeBridge}}
}

func TestPageModemSansMotDePasse(t *testing.T) {
	db := baseRemplie(t, 1)
	h, _ := monter(t, db, essai{Forced: settings.Source{settings.ModemModel: "cga4233"}})
	body := get(t, h, session(t, db), "/modem").Body.String()
	if !strings.Contains(body, "Mot de passe du modem non renseigné") || !strings.Contains(body, "/reglages/modem") {
		t.Errorf("la page doit dire quoi régler : %s", body)
	}
}

func TestPageModemEnPont(t *testing.T) {
	h, token, _ := serveurModem(t, pilotePont())
	rec := get(t, h, token, "/modem")
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d", rec.Code)
	}
	body := rec.Body.String()
	for _, attendu := range []string{"Technicolor CGA4233VOO", "En service", "2" + nbsp + "jours 2" + nbsp + "h", "Pont (bridge)",
		"Mode pont", "/modem/redemarrer", `href="/modem"`} {
		if !strings.Contains(body, attendu) {
			t.Errorf("la page ne contient pas %q", attendu)
		}
	}
	if strings.Contains(body, "Réseau local") {
		t.Error("en pont, le réseau domestique ne se lit pas ici")
	}
}

func TestPageModemEnRouteur(t *testing.T) {
	p := pilotePont()
	p.info.Mode = modem.ModeRouter
	on := true
	p.router = modem.Router{LANAddress: "192.168.0.1", LANMask: "255.255.255.0", DHCP: &on,
		Radios: []modem.Radio{{Band: "2,4 GHz", Enabled: true, SSID: "Maison", Channel: "6", AutoChannel: true, Standard: "b,g,n"},
			{Band: "5 GHz", SSID: "Maison-5G", Hidden: true}},
		Hosts:   []modem.Host{{Name: "portable", IP: "192.168.0.12", Active: true}, {Name: "imprimante"}},
		Missing: []string{"appareils invités"}}
	h, token, _ := serveurModem(t, p)
	body := get(t, h, token, "/modem").Body.String()
	for _, attendu := range []string{"Réseau local", "192.168.0.1 (masque 255.255.255.0)", "Actif : le modem attribue",
		"Wi-Fi 2,4 GHz", "6 (automatique)", "802.11" + nbsp + "b/g/n", "Maison-5G (masqué)", "Désactivé",
		"1 connecté sur 2 connus", "portable", "Absent", "n'a pas rendu : appareils invités", "Lecture seule"} {
		if !strings.Contains(body, attendu) {
			t.Errorf("la page ne contient pas %q", attendu)
		}
	}
}

func TestPageModemPiloteSansGestion(t *testing.T) {
	h, token, _ := serveurModem(t, piloteSimple{})
	body := get(t, h, token, "/modem").Body.String()
	if !strings.Contains(body, "Informations non disponibles") || strings.Contains(body, "/modem/redemarrer") {
		t.Errorf("pilote sans gestion : %s", body)
	}
	if rec := get(t, h, token, "/modem/redemarrer"); rec.Code != http.StatusSeeOther {
		t.Errorf("confirmation sans redémarrage possible : HTTP %d", rec.Code)
	}
}

func TestRedemarrageDemandeConfirmation(t *testing.T) {
	p := pilotePont()
	h, token, db := serveurModem(t, p)
	body := get(t, h, token, "/modem/redemarrer").Body.String()
	if !strings.Contains(body, `action="/modem/redemarrer"`) || !strings.Contains(body, "Annuler") {
		t.Errorf("confirmation : %s", body)
	}
	if p.redemarrages != 0 {
		t.Fatal("un GET ne doit rien redémarrer")
	}

	rec := posteAvec(t, h, token, "/modem/redemarrer", url.Values{})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/modem?redemarrage=demande" {
		t.Fatalf("HTTP %d vers %q", rec.Code, rec.Header().Get("Location"))
	}
	if p.redemarrages != 1 {
		t.Errorf("%d redémarrages", p.redemarrages)
	}
	if _, ok, _ := store.LastEvent(context.Background(), db, events.KindRestartAsked); !ok {
		t.Error("la demande doit figurer au journal")
	}

	// Le modem redemarre : la page ne l'interroge pas.
	p.lectures = 0
	body = get(t, h, token, "/modem?redemarrage=demande").Body.String()
	if !strings.Contains(body, "Le modem redémarre.") || p.lectures != 0 {
		t.Errorf("après la demande : %d lectures, %s", p.lectures, body)
	}

	// Une seconde demande dans la foulee est refusee.
	if body := get(t, h, token, "/modem/redemarrer").Body.String(); !strings.Contains(body, "Redémarrage déjà demandé") ||
		strings.Contains(body, `action="/modem/redemarrer"`) {
		t.Errorf("seconde confirmation : %s", body)
	}
	rec = posteAvec(t, h, token, "/modem/redemarrer", url.Values{})
	if rec.Header().Get("Location") != "/modem?redemarrage=trop-tot" || p.redemarrages != 1 {
		t.Errorf("seconde demande : %q, %d redémarrages", rec.Header().Get("Location"), p.redemarrages)
	}
}

func TestRedemarrageRefuseOuSansReponse(t *testing.T) {
	for err, attendu := range map[error]string{
		modem.ErrRestartRefused: "refuse",
		modem.ErrNoAnswer:       "sans-reponse",
		modem.ErrAuth:           "erreur",
	} {
		p := pilotePont()
		p.restartErr = err
		h, token, db := serveurModem(t, p)
		rec := posteAvec(t, h, token, "/modem/redemarrer", url.Values{})
		if got := rec.Header().Get("Location"); got != "/modem?redemarrage="+attendu {
			t.Errorf("%v : %q", err, got)
		}
		_, noted, _ := store.LastEvent(context.Background(), db, events.KindRestartAsked)
		if noted != (attendu != "erreur") {
			t.Errorf("%v : journal %v", err, noted)
		}
	}
}

func TestRedemarrageRefuseHorsDuSite(t *testing.T) {
	p := pilotePont()
	h, token, _ := serveurModem(t, p)
	req, _ := http.NewRequest(http.MethodPost, "/modem/redemarrer", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || p.redemarrages != 0 {
		t.Errorf("HTTP %d, %d redémarrages", rec.Code, p.redemarrages)
	}
}

func TestProposerMonModem(t *testing.T) {
	release := make(chan struct{})
	faux := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			_, _ = w.Write([]byte(`<html><title>Modem X</title><script src="/app.js"></script> MAC 00:11:22:33:44:55</html>`))
			return
		}
		<-release // le modem repond lentement
		_, _ = w.Write([]byte(`fetch("/cgi-bin/status.cgi")`))
	}))
	t.Cleanup(faux.Close)
	db := baseRemplie(t, 1)
	h, _ := monter(t, db, essai{Forced: settings.Source{settings.ModemURL: faux.URL}})
	token := session(t, db)

	body := get(t, h, token, "/modem/proposer?adresse="+faux.URL).Body.String()
	if !strings.Contains(body, `action="/modem/proposer"`) || !strings.Contains(body, faux.URL) {
		t.Fatalf("page : %s", body)
	}
	rec := posteAvec(t, h, token, "/modem/proposer", url.Values{"adresse": {faux.URL}, "modele": {"Ma Box 3000"}})
	page := rec.Header().Get("Location")
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(page, "/modem/proposer?rapport=") {
		t.Fatalf("HTTP %d vers %q", rec.Code, page)
	}
	// Pendant la lecture : l'etape en cours, et la page se recharge seule.
	deadline := time.Now().Add(5 * time.Second)
	for body = get(t, h, token, page).Body.String(); !strings.Contains(body, "scripts de l&#39;interface web"); body = get(t, h, token, page).Body.String() {
		if time.Now().After(deadline) {
			t.Fatalf("étape attendue : %s", body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(body, "Lecture du modem") || !strings.Contains(body, `http-equiv="refresh"`) || !strings.Contains(body, "<progress") {
		t.Errorf("progression : %s", body)
	}
	// Une seconde demande pendant la lecture rejoint la premiere.
	if again := posteAvec(t, h, token, "/modem/proposer", url.Values{"adresse": {faux.URL}}); again.Header().Get("Location") != page {
		t.Errorf("seconde demande : %q", again.Header().Get("Location"))
	}
	close(release)
	deadline = time.Now().Add(5 * time.Second)
	for body = get(t, h, token, page).Body.String(); !strings.Contains(body, "Rapport prêt"); body = get(t, h, token, page).Body.String() {
		if time.Now().After(deadline) {
			t.Fatalf("rapport attendu : %s", body)
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, attendu := range []string{"data:text/plain;charset=utf-8;base64,", "download=\"mire-modem-", "title=Modem&#43;%3A&#43;Ma&#43;Box&#43;3000",
		"https://github.com/floopich/mire/issues/new?body=", "/cgi-bin/status.cgi", "Modem X"} {
		if !strings.Contains(body, attendu) {
			t.Errorf("la page ne contient pas %q", attendu)
		}
	}
	if strings.Contains(body, "00:11:22:33:44:55") || strings.Contains(body, `http-equiv="refresh"`) {
		t.Error("adresse MAC masquée, et plus de rechargement une fois prêt")
	}
	// Le nom se corrige sur la page : la demande suit.
	if body := get(t, h, token, page+"&modele=Box+Y").Body.String(); !strings.Contains(body, "title=Modem&#43;%3A&#43;Box&#43;Y") {
		t.Error("le nom corrigé doit passer dans la demande")
	}
	if body := get(t, h, token, "/modem/proposer?rapport=inconnu").Body.String(); !strings.Contains(body, "plus disponible") {
		t.Error("un rapport inconnu doit le dire")
	}
	// Une adresse qui n'est pas HTTP est ignoree : celle des reglages la remplace.
	rec = posteAvec(t, h, token, "/modem/proposer", url.Values{"adresse": {"javascript:alert(1)"}})
	if body := get(t, h, token, rec.Header().Get("Location")).Body.String(); strings.Contains(body, "javascript:") || !strings.Contains(body, faux.URL) {
		t.Errorf("adresse invalide : %s", body)
	}
}

func TestModemNonPrisEnCharge(t *testing.T) {
	db := baseRemplie(t, 0)
	h, svc := monter(t, db, essai{Forced: settings.Source{settings.ModemModel: modem.Generic}})
	token := session(t, db)
	if st := svc.State(); !errors.Is(st.ModelErr, modem.ErrUnsupported) || st.Modem != nil {
		t.Fatalf("état %+v", st.ModelErr)
	}
	body := get(t, h, token, "/modem").Body.String()
	for _, attendu := range []string{"non pris en charge", "Proposer mon modem", "Faire prendre en charge"} {
		if !strings.Contains(body, attendu) {
			t.Errorf("page Modem : %q absent", attendu)
		}
	}
	if body := get(t, h, token, "/").Body.String(); !strings.Contains(body, "/modem/proposer") {
		t.Error("la vue d'ensemble doit proposer d'envoyer le modem")
	}
	if body := get(t, h, token, "/modem/proposer").Body.String(); !strings.Contains(body, "<h1 class=\"headline-s\">Proposer mon modem") {
		t.Error("titre de la page")
	}
}

func TestAssistantAvecUnModemNonPrisEnCharge(t *testing.T) {
	db := baseRemplie(t, 0)
	h, _ := monter(t, db, essai{})
	token := session(t, db)
	rec := posteAvec(t, h, token, "/installation/modem", url.Values{"adresse": {"http://192.168.100.1"}, "modele": {modem.Generic}})
	if body := rec.Body.String(); !strings.Contains(body, "Modem non pris en charge") || !strings.Contains(body, "/modem/proposer") {
		t.Errorf("assistant : %s", body)
	}
}

func TestLecturelenteDuModem(t *testing.T) {
	defer func(d time.Duration) { quickWait = d }(quickWait)
	quickWait = 20 * time.Millisecond
	p := pilotePont()
	p.lent = 300 * time.Millisecond
	h, token, _ := serveurModem(t, p)

	rec := get(t, h, token, "/modem")
	loc := rec.Header().Get("Location")
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(loc, "/modem?lecture=") {
		t.Fatalf("HTTP %d vers %q", rec.Code, loc)
	}
	body := get(t, h, token, loc).Body.String()
	if !strings.Contains(body, "Lecture du modem…") || !strings.Contains(body, `http-equiv="refresh"`) {
		t.Errorf("attente : %s", body)
	}
	time.Sleep(500 * time.Millisecond)
	body = get(t, h, token, loc).Body.String()
	if strings.Contains(body, "Lecture du modem…") || strings.Contains(body, `http-equiv="refresh"`) || !strings.Contains(body, "Pont (bridge)") {
		t.Errorf("résultat : %s", body)
	}
}

func TestEssaiLentDuModem(t *testing.T) {
	defer func(d time.Duration) { quickWait = d }(quickWait)
	quickWait = 20 * time.Millisecond
	p := pilotePont()
	p.lent = 300 * time.Millisecond
	h, token, _ := serveurModem(t, p)

	rec := posteAvec(t, h, token, "/reglages/modem/tester", url.Values{})
	loc := rec.Header().Get("Location")
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(loc, "/reglages/modem?essai=") {
		t.Fatalf("HTTP %d vers %q", rec.Code, loc)
	}
	if body := get(t, h, token, loc).Body.String(); !strings.Contains(body, "Connexion au modem et lecture des canaux…") {
		t.Errorf("attente : %s", body)
	}
	time.Sleep(500 * time.Millisecond)
	if body := get(t, h, token, loc).Body.String(); !strings.Contains(body, "Le modem répond") || strings.Contains(body, `http-equiv="refresh"`) {
		t.Errorf("résultat : %s", body)
	}
}
