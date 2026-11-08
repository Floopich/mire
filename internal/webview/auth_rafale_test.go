package webview

import (
	"net/http"
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
