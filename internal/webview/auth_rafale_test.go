package webview

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"sync"
	"testing"
)

func TestUneRafaleDeConnexionsEstFreinee(t *testing.T) {
	// Quarante essais simultanes depuis la meme adresse : trois au plus
	// sont verifies, les autres sont freines avant toute verification.
	db := baseRemplie(t, 1)
	h, _ := monter(t, db, essai{Collecting: true})
	var mu sync.Mutex
	codes := map[int]int{}
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := posteAvec(t, h, "", "/connexion", url.Values{"mot_de_passe": {"faux-" + string(rune('a'+i%26))}})
			mu.Lock()
			codes[rec.Code]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if codes[http.StatusUnauthorized] > 3 || codes[http.StatusTooManyRequests] < 37 {
		t.Errorf("réponses %v : au plus 3 essais vérifiés", codes)
	}
}

func TestAdresseDuClientDerriereUnProxy(t *testing.T) {
	proxy := netip.MustParsePrefix("172.18.0.0/16")
	s := &Server{opts: Options{TrustedProxies: []netip.Prefix{proxy}}}
	cas := []struct {
		remote, xff, want string
	}{
		{"192.168.1.20:5000", "", "192.168.1.20"},                       // sans proxy
		{"192.168.1.20:5000", "1.2.3.4", "192.168.1.20"},                // en-tete d'un client direct : ignore
		{"172.18.0.3:5000", "192.168.1.20", "192.168.1.20"},             // derriere le proxy
		{"172.18.0.3:5000", "6.6.6.6, 192.168.1.20", "192.168.1.20"},    // ce que le client ecrit a gauche ne compte pas
		{"172.18.0.3:5000", "192.168.1.20, 172.18.0.9", "192.168.1.20"}, // proxys en chaine
		{"172.18.0.3:5000", "", "172.18.0.3"},                           // proxy sans en-tete
		{"172.18.0.3:5000", "n'importe quoi", "172.18.0.3"},             // en-tete illisible
		{"[::ffff:172.18.0.3]:5000", "192.168.1.20", "192.168.1.20"},    // IPv4 en IPv6
	}
	for _, c := range cas {
		r := httptest.NewRequest(http.MethodPost, "/connexion", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := s.clientKey(r); got != c.want {
			t.Errorf("%s / %q : %s, attendu %s", c.remote, c.xff, got, c.want)
		}
	}
	// Sans proxy declare, rien n'est cru.
	r := httptest.NewRequest(http.MethodPost, "/connexion", nil)
	r.RemoteAddr = "172.18.0.3:5000"
	r.Header.Set("X-Forwarded-For", "192.168.1.20")
	if got := (&Server{}).clientKey(r); got != "172.18.0.3" {
		t.Errorf("sans proxy déclaré : %s", got)
	}
}
