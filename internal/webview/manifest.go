package webview

import (
	"encoding/json"
	"net/http"
	"time"
)

// Le manifeste rend Mire installable comme une application. Il se sert sans
// session et ne contient rien de la ligne. Pas de service worker.

type manifestIcon struct {
	Src     string `json:"src"`
	Sizes   string `json:"sizes"`
	Type    string `json:"type"`
	Purpose string `json:"purpose,omitempty"`
}

type manifestShortcut struct {
	Name  string         `json:"name"`
	URL   string         `json:"url"`
	Icons []manifestIcon `json:"icons,omitempty"`
}

type manifest struct {
	ID              string             `json:"id"`
	Name            string             `json:"name"`
	ShortName       string             `json:"short_name"`
	Description     string             `json:"description"`
	Lang            string             `json:"lang"`
	Dir             string             `json:"dir"`
	StartURL        string             `json:"start_url"`
	Scope           string             `json:"scope"`
	Display         string             `json:"display"`
	BackgroundColor string             `json:"background_color"`
	ThemeColor      string             `json:"theme_color"`
	Categories      []string           `json:"categories"`
	Icons           []manifestIcon     `json:"icons"`
	Shortcuts       []manifestShortcut `json:"shortcuts"`
}

// corail est la couleur de Mire : barres systeme de l'application installee,
// ecran de lancement, theme-color de chaque page.
const corail = "#E8817F"

func appManifest() manifest {
	return manifest{
		ID:              "/",
		Name:            "Mire",
		ShortName:       "Mire",
		Description:     "Analyse en continu d'une ligne câble DOCSIS.",
		Lang:            "fr",
		Dir:             "ltr",
		StartURL:        "/",
		Scope:           "/",
		Display:         "standalone",
		BackgroundColor: corail,
		ThemeColor:      corail,
		Categories:      []string{"utilities"},
		Icons: []manifestIcon{
			{Src: "/static/icone-192.png", Sizes: "192x192", Type: "image/png", Purpose: "any"},
			{Src: "/static/icone-512.png", Sizes: "512x512", Type: "image/png", Purpose: "any"},
			{Src: "/static/icone-masquable-512.png", Sizes: "512x512", Type: "image/png", Purpose: "maskable"},
		},
		Shortcuts: []manifestShortcut{
			{Name: "Réglages", URL: "/reglages"},
		},
	}
}

// manifestHandler sert le manifeste, calcule une fois.
func manifestHandler() http.Handler {
	body, err := json.Marshal(appManifest())
	if err != nil {
		panic(err) // structure fixe : une erreur ici est une erreur de code
	}
	return cacheFor(24*time.Hour, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/manifest+json")
		_, _ = w.Write(body)
	}))
}
