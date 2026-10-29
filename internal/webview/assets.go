package webview

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"strconv"
	"time"
)

// Les feuilles de style sont appelees avec l'empreinte de leur contenu
// (/static/mire.css?v=…). Une nouvelle version de Mire change donc leur
// adresse : un navigateur ne garde jamais une feuille perimee face a une page
// recente, et une adresse versionnee peut etre conservee un an.
//
// La police et les icones gardent une adresse fixe : la feuille de style et
// le manifeste les designent ainsi. Elles changent rarement et restent en
// cache un jour.

// versionedPaths sont les ressources appelees avec leur empreinte.
var versionedPaths = []string{"/static/mire.css", "/reglages.css"}

const (
	staticMaxAge    = 24 * time.Hour
	versionedMaxAge = 365 * 24 * time.Hour
)

func staticFS() fs.FS {
	sub, err := fs.Sub(assetFS, "assets")
	if err != nil {
		panic(err) // le dossier est embarque a la compilation
	}
	return sub
}

func fingerprint(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])[:12]
}

// assetVersions calcule l'empreinte de chaque ressource versionnee.
func assetVersions(generated map[string][]byte) map[string]string {
	v := map[string]string{}
	for _, p := range versionedPaths {
		b, ok := generated[p]
		if !ok {
			var err error
			if b, err = fs.ReadFile(staticFS(), p[len("/static/"):]); err != nil {
				panic(err) // ressource declaree mais absente : erreur de compilation
			}
		}
		v[p] = fingerprint(b)
	}
	return v
}

// assetURL rend l'adresse versionnee d'une ressource.
func (s *Server) assetURL(path string) string {
	if v, ok := s.versions[path]; ok {
		return path + "?v=" + v
	}
	return path
}

// cacheAssets fixe la duree de cache : un an pour l'adresse versionnee
// courante, la duree par defaut sinon.
func (s *Server) cacheAssets(fallback time.Duration, next http.Handler) http.Handler {
	long := "public, max-age=" + strconv.Itoa(int(versionedMaxAge.Seconds())) + ", immutable"
	short := "public, max-age=" + strconv.Itoa(int(fallback.Seconds()))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := short
		if v := r.URL.Query().Get("v"); v != "" && v == s.versions[r.URL.Path] {
			value = long
		}
		w.Header().Set("Cache-Control", value)
		next.ServeHTTP(w, r)
	})
}
