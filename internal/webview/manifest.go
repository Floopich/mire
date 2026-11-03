package webview

import (
	"encoding/json"
	"net/http"

	"github.com/floopich/mire/internal/settings"
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

// iconPrefix rend le debut du nom des icones d'un style ; le style classique
// garde les noms historiques. Un style inconnu revient au classique.
func iconPrefix(style string) string {
	for _, o := range settings.AppIcons() {
		if o.Value == style && style != "classique" {
			return "/static/icone-" + style + "-"
		}
	}
	return "/static/icone-"
}

// iconBackground est le fond de la tuile, repris par l'ecran de lancement.
var iconBackground = map[string]string{"sombre": "#201A19", "inversee": corail, "noir-blanc": "#FFFFFF"}

func appManifest(style string) manifest {
	pre := iconPrefix(style)
	bg := corail
	if c, ok := iconBackground[style]; ok {
		bg = c
	}
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
		BackgroundColor: bg,
		ThemeColor:      corail,
		Categories:      []string{"utilities"},
		Icons: []manifestIcon{
			{Src: pre + "192.png", Sizes: "192x192", Type: "image/png", Purpose: "any"},
			{Src: pre + "512.png", Sizes: "512x512", Type: "image/png", Purpose: "any"},
			{Src: pre + "masquable-512.png", Sizes: "512x512", Type: "image/png", Purpose: "maskable"},
			// Icones a theme d'Android : le systeme teinte la silhouette.
			{Src: "/static/icone-monochrome-512.png", Sizes: "512x512", Type: "image/png", Purpose: "monochrome"},
		},
		Shortcuts: []manifestShortcut{
			{Name: "Réglages", URL: "/reglages"},
		},
	}
}

// handleManifest sert le manifeste du style choisi. Le navigateur le relit
// pour mettre a jour l'application installee : il n'est pas garde en cache.
func (s *Server) handleManifest(w http.ResponseWriter, _ *http.Request) {
	body, err := json.Marshal(appManifest(s.iconStyle()))
	if err != nil {
		http.Error(w, "manifeste impossible", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(body)
}

// iconStyle est le style d'icone en vigueur.
func (s *Server) iconStyle() string {
	return s.opts.Service.State().Values.String(settings.AppIcon)
}

// appleIcon est l'icone de l'ecran d'accueil d'iOS, lue a l'ajout seulement.
func (s *Server) appleIcon() string { return iconPrefix(s.iconStyle()) + "apple-180.png" }
