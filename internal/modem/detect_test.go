package modem

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serveur(t *testing.T, page string) string {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(page))
	}))
	t.Cleanup(s.Close)
	return strings.TrimPrefix(s.URL, "http://")
}

func TestDetectionReconnaitLeModemEtClasseLesAppareils(t *testing.T) {
	routeur := serveur(t, `<html><head><title>Box Internet</title></head></html>`)
	inconnu := serveur(t, `<html><title>Modem X</title><p>DOCSIS 3.1</p></html>`)
	voo := serveur(t, `<html><head><title>  Technicolor
	CGA4233 </title><link href="/theme/voo/style.css"></head></html>`)
	ferme := "127.0.0.1:1" // rien n'ecoute

	got := Detect(context.Background(), []string{routeur, ferme, inconnu, voo})
	if len(got) != 3 {
		t.Fatalf("%d appareils, attendu 3 : %+v", len(got), got)
	}
	if got[0].Model == nil || got[0].Model.ID != "cga4233" {
		t.Errorf("le modem reconnu doit venir en premier : %+v", got[0])
	}
	if got[0].Title != "Technicolor CGA4233" {
		t.Errorf("titre %q, attendu nettoye", got[0].Title)
	}
	if got[1].Model != nil || !got[1].DOCSIS {
		t.Errorf("le modem DOCSIS inconnu doit venir ensuite : %+v", got[1])
	}
	if got[2].DOCSIS || got[2].Title != "Box Internet" {
		t.Errorf("l'appareil non DOCSIS doit venir en dernier : %+v", got[2])
	}
}

func TestPasserelleParDefaut(t *testing.T) {
	route := "Iface\tDestination\tGateway\tFlags\n" +
		"eth0\t0064A8C0\t00000000\t0001\n" +
		"eth0\t00000000\t0164A8C0\t0003\n"
	if gw := parseRoute(strings.NewReader(route)); gw != "192.168.100.1" {
		t.Errorf("passerelle %q, attendu 192.168.100.1", gw)
	}
	if gw := parseRoute(strings.NewReader("Iface\tDestination\tGateway\n")); gw != "" {
		t.Errorf("sans route par defaut, attendu vide : %q", gw)
	}
}

func TestListeDesAdressesSansDoublon(t *testing.T) {
	hosts := Hosts("http://192.168.100.1/", "10.0.0.1")
	vus := map[string]bool{}
	for _, h := range hosts {
		if vus[h] {
			t.Errorf("%s en double dans %v", h, hosts)
		}
		vus[h] = true
	}
	if hosts[0] != "192.168.100.1" || hosts[1] != "10.0.0.1" {
		t.Errorf("les adresses demandees viennent en tete : %v", hosts)
	}
}

func TestDetectionSuitLaRedirectionVersLaPageDeConnexion(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/login.html", http.StatusFound)
	})
	mux.HandleFunc("/login.html", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<title>Technicolor CGA4233</title><script src="/theme/voo/app.js"></script>`))
	})
	s := httptest.NewServer(mux)
	defer s.Close()

	got := Detect(context.Background(), []string{strings.TrimPrefix(s.URL, "http://")})
	if len(got) != 1 || got[0].Model == nil {
		t.Fatalf("modem non reconnu derriere une redirection : %+v", got)
	}
}
