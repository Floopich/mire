package modem

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// Le CGA4233 offre toute la gestion.
var (
	_ InfoReader   = (*CGA4233)(nil)
	_ RouterReader = (*CGA4233)(nil)
	_ Restarter    = (*CGA4233)(nil)
	_ Capturer     = (*CGA4233)(nil)
)

const reponseSysteme = `{"error":"ok","message":"all values retrieved","data":{
 "ModelName":"CGA4233VOO","Manufacturer":"Technicolor","HardwareVersion":"1.0",
 "SoftwareVersion":"CGA4233VOO-1.2.3","FirmwareName":"ignore","UpTime":"273784",
 "LanMode":"bridge-static","CMStatus":"OPERATIONAL"}}`

const reponseDHCP = `{"error":"ok","message":"","data":{"IPAddressRT":["192.168.0.1"],"SubnetMaskRT":"255.255.255.0",
 "PoolEnable":"true","DNSTblRT":[{"__id":"1","DNSIPAddress":"192.168.0.1"},{"__id":"2","DNSIPAddress":"0.0.0.0"}]}}`

const reponseWifi = `{"1":{"error":"ok","message":"","data":{"RadioEnable":"true","SSIDEnable":"true","SSID":"Maison",
 "SSIDAdvertisementEnabled":"true","Channel":"6","AutoChannelEnable":"true","OperatingChannelBandwidth":"20MHz",
 "OperatingStandards":"b,g,n","ModeEnabled":"WPA2-Personal"}},
 "2":{"error":"ok","message":"","data":{"RadioEnable":"false","SSID":"Maison-5G","SSIDAdvertisementEnabled":"false",
 "Channel":"36","OperatingStandards":"a,n,ac"}}}`

const reponseAppareils = `{"error":"ok","message":"","data":{"hostTbl":[
 {"hostname":"imprimante","ipaddress":"192.168.0.20","physaddress":"AA:BB:CC:00:11:22","active":"false"},
 {"hostname":"portable","ipaddress":"192.168.0.12","physaddress":"AA:BB:CC:00:11:33","active":"true"}]}}`

func modemGere(t *testing.T) (*fauxModem, *CGA4233) {
	t.Helper()
	f := &fauxModem{motDePasse: "secret", sansToken: true, pages: map[string]string{
		systemPath: reponseSysteme,
		dhcpPath:   reponseDHCP,
		wifiPath:   reponseWifi,
		hostPath:   reponseAppareils,
	}}
	return f, piloteVers(t, f)
}

func TestInfoLitLeModeleEtLeMode(t *testing.T) {
	_, m := modemGere(t)
	info, err := m.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Model != "CGA4233VOO" || info.Software != "CGA4233VOO-1.2.3" || info.Status != "OPERATIONAL" {
		t.Errorf("informations %+v", info)
	}
	if info.Mode != ModeBridge {
		t.Errorf("mode %q, attendu pont", info.Mode)
	}
	if info.Uptime != 273784*time.Second {
		t.Errorf("durée %v", info.Uptime)
	}
}

func TestUneSessionExpireeEnStatut200EstRejouee(t *testing.T) {
	f, m := modemGere(t)
	if _, err := m.Info(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.expireUneFois = true
	if _, err := m.Info(context.Background()); err != nil {
		t.Fatalf("après expiration : %v", err)
	}
	if f.logins != 2 {
		t.Errorf("%d authentifications, attendu 2", f.logins)
	}
}

func TestRouterLitReseauWifiEtAppareils(t *testing.T) {
	_, m := modemGere(t)
	r, err := m.Router(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.LANAddress != "192.168.0.1" || r.LANMask != "255.255.255.0" || r.DHCP == nil || !*r.DHCP {
		t.Errorf("réseau local %+v", r)
	}
	if len(r.DNS) != 1 || r.DNS[0] != "192.168.0.1" {
		t.Errorf("DNS %v : l'adresse nulle doit être écartée", r.DNS)
	}
	if len(r.Radios) != 2 {
		t.Fatalf("%d radios", len(r.Radios))
	}
	if r.Radios[0].Band != "2,4 GHz" || !r.Radios[0].Enabled || r.Radios[0].SSID != "Maison" || r.Radios[0].Hidden {
		t.Errorf("radio 1 %+v", r.Radios[0])
	}
	if r.Radios[1].Band != "5 GHz" || r.Radios[1].Enabled || !r.Radios[1].Hidden {
		t.Errorf("radio 2 %+v", r.Radios[1])
	}
	if len(r.Hosts) != 2 || r.Hosts[0].Name != "portable" || !r.Hosts[0].Active || r.Hosts[1].MAC != "aa:bb:cc:00:11:22" {
		t.Errorf("appareils %+v : les actifs d'abord", r.Hosts)
	}
	if len(r.Missing) != 0 {
		t.Errorf("parties manquantes %v", r.Missing)
	}
}

func TestUnePartieIllisibleNEmpechePasLesAutres(t *testing.T) {
	f, m := modemGere(t)
	delete(f.pages, hostPath)
	r, err := m.Router(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Missing) != 1 || r.Missing[0] != "appareils" || len(r.Radios) != 2 {
		t.Errorf("%+v", r)
	}
	f.pages = map[string]string{}
	if _, err := m.Router(context.Background()); err == nil {
		t.Error("tout illisible doit être une erreur")
	}
}

func TestRedemarrageCommeLInterfaceVOO(t *testing.T) {
	f, m := modemGere(t)
	if err := m.Restart(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.redemarrages != 1 {
		t.Errorf("%d redémarrages", f.redemarrages)
	}
	// Exactement le bouton « Redémarrer le système » de views/adm_reset.html ;
	// la cle « reset » serait une reinitialisation d'usine.
	want := url.Values{"reboot": {"Router,Wifi,VoIP,Dect,MoCA"}, "ui_access": {"reboot_device"}}
	if f.formulaire.Encode() != want.Encode() || f.formulaire.Has("reset") {
		t.Errorf("formulaire %v", f.formulaire)
	}
	// La session meurt avec le modem : le releve suivant se reconnecte.
	if _, err := m.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.logins != 2 {
		t.Errorf("%d authentifications, attendu 2", f.logins)
	}
}

func TestRedemarrageRefuse(t *testing.T) {
	f, m := modemGere(t)
	f.refuse = true
	err := m.Restart(context.Background())
	if !errors.Is(err, ErrRestartRefused) || !strings.Contains(err.Error(), "MSG_REFUS") {
		t.Errorf("erreur %v", err)
	}
}

func TestRedemarrageSansReponseNEstPasRejoue(t *testing.T) {
	f, m := modemGere(t)
	f.coupe = true
	err := m.Restart(context.Background())
	if !errors.Is(err, ErrNoAnswer) {
		t.Errorf("erreur %v", err)
	}
	if n := f.demandes.Load(); n != 1 {
		t.Errorf("%d demandes envoyées, attendu 1", n)
	}
}

func TestLesAppelsConcurrentsPassentUnParUn(t *testing.T) {
	_, m := modemGere(t)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = m.Snapshot(context.Background()) }()
		go func() { defer wg.Done(); _, _ = m.Info(context.Background()) }()
	}
	wg.Wait()
}

func TestCaptureAnonymiseeEtEnLectureSeule(t *testing.T) {
	f, m := modemGere(t)
	f.pages[capturePaths[0].path] = `{"error":"ok","data":{"ModelName":"CGA4233VOO","SerialNumber":"CP1234567","CMMACAddress":"AA:BB:CC:DD:EE:FF"}}`
	caps, err := m.Capture(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.ecritures) != 0 {
		t.Errorf("le diagnostic a écrit : %v", f.ecritures)
	}
	out, _ := json.Marshal(caps)
	for _, secret := range []string{"CP1234567", "AA:BB:CC:DD:EE:FF", "secret"} {
		if strings.Contains(string(out), secret) {
			t.Errorf("%q figure dans la capture : %s", secret, out)
		}
	}
	if !strings.Contains(string(out), "CGA4233VOO") {
		t.Error("le modèle doit rester lisible")
	}
	var absent, ui, screen *Capture
	for i := range caps {
		switch caps[i].Path {
		case "écran /views/adm_reset.html":
			screen = &caps[i]
		case "/api/v1/login_conf":
			absent = &caps[i]
		case "interface web : chemins appelés par les scripts":
			ui = &caps[i]
		}
	}
	if screen == nil || !strings.Contains(screen.Text, `reboot:"Router,Wifi,VoIP,Dect,MoCA"`) {
		t.Errorf("l'écran de redémarrage doit être rendu en entier : %+v", screen)
	}
	if absent == nil || absent.Status != 404 {
		t.Errorf("un chemin absent doit être noté avec son statut : %+v", absent)
	}
	if ui == nil || !strings.Contains(ui.Text, "api/v1/sta_restart") || !strings.Contains(ui.Text, "/js/app.js") ||
		!strings.Contains(ui.Text, "/pages/redemarrage.html") || !strings.Contains(ui.Text, "api/v1/reset") ||
		strings.Contains(ui.Text, "cdn.exemple") {
		t.Errorf("chemins de l'interface : %+v", ui)
	}
}

func TestAnonymize(t *testing.T) {
	in := `{"data":{"ModelName":"CGA","SerialNumber":"X1","BootloaderVersion":"2.4.0.18","SSID":"Chez Moi","hostTbl":[{"hostname":"pc","ipaddress":"192.168.0.2"}],
 "WanIP":"203.0.113.7","Mask":"255.255.255.0","Note":"mac 00:11:22:33:44:55 via 2001:db8:1:2::5","Port":1234}}`
	out := string(Anonymize([]byte(in)))
	for _, gone := range []string{"X1", "Chez Moi", `"pc"`, "203.0.113.7", "00:11:22:33:44:55", "2001:db8"} {
		if strings.Contains(out, gone) {
			t.Errorf("%q reste : %s", gone, out)
		}
	}
	for _, kept := range []string{"CGA", "192.168.0.2", "255.255.255.0", "1234", "2.4.0.18"} {
		if !strings.Contains(out, kept) {
			t.Errorf("%q a disparu : %s", kept, out)
		}
	}
	if got := string(Anonymize([]byte("pas du json 203.0.113.7"))); got != "pas du json x.x.x.x" {
		t.Errorf("texte : %q", got)
	}
}

func TestParseUptime(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"3600":              time.Hour,
		"04:05:06":          4*time.Hour + 5*time.Minute + 6*time.Second,
		"3 days 04:05:06":   3*24*time.Hour + 4*time.Hour + 5*time.Minute + 6*time.Second,
		"2d 3h 4m 5s":       2*24*time.Hour + 3*time.Hour + 4*time.Minute + 5*time.Second,
		"1 jour 2 heures":   26 * time.Hour,
		"":                  0,
		"inconnu":           0,
		"x 04:05:06 ":       0,
		"12 min":            12 * time.Minute,
		"0":                 0,
		"5 hours 3 minutes": 5*time.Hour + 3*time.Minute,
	} {
		if got := parseUptime(in); got != want {
			t.Errorf("parseUptime(%q) = %v, attendu %v", in, got, want)
		}
	}
}

func TestRapportDUnModemInconnuSansIdentifiants(t *testing.T) {
	f := &fauxModem{motDePasse: "secret"}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	r := BuildReport(context.Background(), srv.URL+"/", nil, nil)
	if f.logins != 0 {
		t.Error("sans pilote, aucune connexion ne doit être tentée")
	}
	if len(r.Lectures) != 2 || r.Lectures[0].Path != "page d'accueil" || r.Lectures[0].Status != 200 {
		t.Fatalf("lectures %+v", r.Lectures)
	}
	if !strings.Contains(r.Lectures[1].Text, "api/v1/sta_restart") || !strings.Contains(r.Lectures[1].Text, "api/v1/system/ModelName") {
		t.Errorf("chemins : %s", r.Lectures[1].Text)
	}
	link := IssueLink(r, "", "VOO")
	if !strings.HasPrefix(link, reportIssueURL) || !strings.Contains(link, "VOO") || len(link) > maxIssueURL {
		t.Errorf("lien %q", link)
	}
}

func TestRapportAvecLePiloteAjouteSesLectures(t *testing.T) {
	f, m := modemGere(t)
	var steps []string
	last, total := -1, 0
	r := BuildReport(context.Background(), m.baseURL, m, func(done, n int, step string) {
		if done < last || done >= n {
			t.Errorf("progression %d/%d après %d", done, n, last)
		}
		last, total = done, n
		steps = append(steps, step)
	})
	if total != 2+len(capturePaths)+1 || !strings.Contains(strings.Join(steps, ","), "canaux DOCSIS") {
		t.Errorf("étapes %v sur %d", steps, total)
	}
	if r.Software != "CGA4233VOO-1.2.3" {
		t.Errorf("logiciel %q", r.Software)
	}
	if len(r.Lectures) != 1+len(capturePaths)+2 || f.logins == 0 {
		t.Errorf("%d lectures, %d connexions", len(r.Lectures), f.logins)
	}
}

func TestLaDemandeTientDansUneAdresseEtContientLEssentiel(t *testing.T) {
	_, m := modemGere(t)
	r := BuildReport(context.Background(), m.baseURL, m, nil)
	r.Mire = "v1.0.0"
	link := IssueLink(r, "", "VOO / Orange")
	if len(link) > maxIssueURL {
		t.Fatalf("adresse de %d caractères", len(link))
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Has("template") || q.Get("title") != "Modem : Technicolor CGA4233VOO" {
		t.Errorf("champs %v", q)
	}
	for _, attendu := range []string{"**Modèle** : Technicolor CGA4233VOO", "**Logiciel** : CGA4233VOO-1.2.3",
		"**Opérateur** : VOO / Orange", "```text\n"} {
		if !strings.Contains(q.Get("body"), attendu) {
			t.Errorf("le texte ne contient pas %q", attendu)
		}
	}
	resume := q.Get("body")
	for _, attendu := range []string{"Rapport de Mire v1.0.0", "Modem : Technicolor CGA4233VOO", "HTTP 200",
		"DSTbl : 2 éléments", "USTbl : 1 élément\n", "DSTbl n°1 : ChannelID=1", "/api/v1/login_conf : HTTP 404", "api/v1/sta_restart"} {
		if !strings.Contains(resume, attendu) {
			t.Errorf("le résumé ne contient pas %q :\n%s", attendu, resume)
		}
	}
	if s := Summary(r, 300); len(s) > 300+60 || !strings.Contains(s, "rapport complet") {
		t.Errorf("budget non tenu : %d caractères\n%s", len(s), s)
	}
}

func TestRedemarrageSansConfirmationHTTP500(t *testing.T) {
	f, m := modemGere(t)
	f.erreur500 = true
	err := m.Restart(context.Background())
	if !errors.Is(err, ErrNoAnswer) || errors.Is(err, ErrRestartRefused) || f.demandes.Load() != 1 {
		t.Errorf("erreur %v, %d demandes : une erreur HTTP sans refus explicite n'est pas un refus", err, f.demandes.Load())
	}
}
