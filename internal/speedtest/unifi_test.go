package speedtest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/store"
)

// passerelle joue une passerelle UniFi : connexion par compte local ou par
// cle API, puis le tableau de bord de UniFi Network 11.
func passerelle(t *testing.T, at *time.Time, connexions *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/login":
			var c map[string]string
			_ = json.NewDecoder(r.Body).Decode(&c)
			if c["username"] != "lecteur" || c["password"] != "secret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			*connexions++
			http.SetCookie(w, &http.Cookie{Name: "TOKEN", Value: "session", Path: "/"})
		case "/proxy/network/api/s/default/get/setting":
			fmt.Fprint(w, `{"meta":{"rc":"ok"},"data":[{"key":"mgmt"},{"key":"auto_speedtest","enabled":false,"cron_expr":"0 0 * * *"}]}`)
		case "/proxy/network/v2/api/site/default/aggregated-dashboard":
			if c, err := r.Cookie("TOKEN"); (err != nil || c.Value != "session") && r.Header.Get("X-API-KEY") != "cle" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			// La sortie reelle d'une Cloud Gateway Ultra : un acces de secours
			// sans test, l'acces principal avec son dernier test.
			fmt.Fprintf(w, `{"speed_test":{"is_supported":true,"data":[
				{"download_mbps":0,"interface_name":"eth0","latency_ms":0,"upload_mbps":0,"wan_networkgroup":"WAN2"},
				{"download_mbps":943,"interface_name":"eth4","latency_ms":24,"time":%d,"upload_mbps":56,"wan_networkgroup":"WAN"}]},
				"isp_metrics":{"no_stats_reason":"no_data"}}`, at.UnixMilli())
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDernierTestDeLaPasserelle(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	at := time.Date(2026, 10, 27, 22, 1, 42, 358_000_000, time.UTC)
	connexions := 0
	srv := passerelle(t, &at, &connexions)

	// Compte local : connexion, puis le dernier test de l'acces principal.
	f := &Fetcher{DB: db, Source: NewUniFi(srv.URL, "", "lecteur", "secret")}
	if n, err := f.Update(ctx); err != nil || n != 1 {
		t.Fatalf("première lecture : %d, %v", n, err)
	}
	got, _ := store.SpeedTests(ctx, db, at.Add(-time.Minute), at.Add(time.Minute))
	if len(got) != 1 || got[0].Download != 943 || got[0].Upload != 56 || *got[0].Ping != 24 ||
		got[0].Server != "Passerelle UniFi, WAN" || !got[0].Time.Equal(at.Truncate(time.Second)) {
		t.Errorf("mesure : %+v", got)
	}
	// Le meme test n'est pas compte deux fois ; un nouveau test l'est.
	if n, _ := f.Update(ctx); n != 0 {
		t.Errorf("doublon : %d", n)
	}
	at = at.Add(time.Hour)
	if n, _ := f.Update(ctx); n != 1 || connexions != 1 {
		t.Errorf("test suivant : %d mesures, %d connexions", n, connexions)
	}

	// Les tests automatiques de la passerelle.
	if on, cron, err := NewUniFi(srv.URL, "cle", "", "").Schedule(ctx); err != nil || on || cron != "0 0 * * *" {
		t.Errorf("programme : %v %q %v", on, cron, err)
	}

	// Cle API : sans connexion.
	if _, err := NewUniFi(srv.URL, "cle", "", "").Since(ctx, time.Time{}, 1); err != nil {
		t.Errorf("clé API : %v", err)
	}
	for src, msg := range map[*UniFi]string{
		NewUniFi(srv.URL, "fausse", "", ""):      "refuse la clé API",
		NewUniFi(srv.URL, "", "lecteur", "faux"): "refuse l'identifiant ou le mot de passe",
		NewUniFi(srv.URL, "", "", ""):            "clé API ou compte local",
		NewUniFi(srv.URL+"/x", "cle", "", ""):    "UniFi Network ne répond pas",
	} {
		if _, err := src.Since(ctx, time.Time{}, 1); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%s : %v, attendu %q", src.URL, err, msg)
		}
	}
}

func TestPasserelleMuette(t *testing.T) {
	_, err := NewUniFi("https://127.0.0.1:1", "cle", "", "").Since(context.Background(), time.Time{}, 1)
	if err == nil || !strings.Contains(err.Error(), "sans port, par exemple https://127.0.0.1") {
		t.Errorf("port en trop : %v", err)
	}
}

func TestAdresseDeLaPasserelle(t *testing.T) {
	if u := NewUniFi("http://192.168.1.1/", "", "", ""); u.URL != "https://192.168.1.1" {
		t.Errorf("adresse : %q", u.URL)
	}
	for host, ok := range map[string]bool{"192.168.10.1": true, "10.0.0.1": true, "unifi": true, "unifi.local": true,
		"8.8.8.8": false, "unifi.exemple.be": false} {
		if local(host) != ok {
			t.Errorf("%s : local %v", host, !ok)
		}
	}
}
