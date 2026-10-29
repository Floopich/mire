package webview

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// Une feuille de style est appelee avec son empreinte : apres une mise a
// jour, le navigateur charge la nouvelle au lieu de garder l'ancienne.
func TestFeuillesDeStyleVersionnees(t *testing.T) {
	h, cookie := serveurAvec(t, 3, essai{Collecting: true})
	for page, feuille := range map[string]string{"/": "/static/mire.css", "/reglages/analyse": "/reglages.css"} {
		rec := get(t, h, cookie, page)
		m := regexp.MustCompile(`href="(` + regexp.QuoteMeta(feuille) + `\?v=[0-9a-f]{12})"`).FindStringSubmatch(rec.Body.String())
		if m == nil {
			t.Errorf("%s : %s appelee sans empreinte", page, feuille)
			continue
		}
		res := get(t, h, cookie, strings.ReplaceAll(m[1], "&amp;", "&"))
		if res.Code != http.StatusOK || !strings.Contains(res.Header().Get("Cache-Control"), "immutable") {
			t.Errorf("%s : %d, cache %q", m[1], res.Code, res.Header().Get("Cache-Control"))
		}
		// Une empreinte perimee n'est pas gardee un an.
		if c := get(t, h, cookie, feuille+"?v=ancienne").Header().Get("Cache-Control"); strings.Contains(c, "immutable") {
			t.Errorf("%s : empreinte perimee gardee (%q)", feuille, c)
		}
	}
}
