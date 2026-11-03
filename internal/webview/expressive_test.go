package webview

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/floopich/mire/internal/settings"
	"image/png"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/analyze"
)

func TestChaqueNiveauASaPropreForme(t *testing.T) {
	// La forme double la couleur : deux niveaux ne doivent jamais partager
	// la meme silhouette.
	vues := map[string]analyze.Level{}
	for _, l := range []analyze.Level{analyze.Good, analyze.Tolerated, analyze.Warning, analyze.Critical, analyze.Unknown} {
		d := shapeD(l)
		if !strings.HasPrefix(d, "M") || !strings.HasSuffix(d, "Z") {
			t.Errorf("%v : contour mal forme", l)
		}
		if autre, ok := vues[d]; ok {
			t.Errorf("%v et %v ont la meme forme", l, autre)
		}
		vues[d] = l
	}
}

func TestLaPoliceEstEmbarqueeEtAutorisee(t *testing.T) {
	h, _ := serveurAvec(t, 1, essai{})
	rec := get(t, h, "", "/static/roboto-flex.woff2")
	if rec.Code != http.StatusOK || rec.Body.Len() < 10_000 {
		t.Fatalf("police : HTTP %d, %d octets", rec.Code, rec.Body.Len())
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "font-src 'self'") {
		t.Errorf("la CSP n'autorise pas la police embarquee : %q", csp)
	}
	if rec := get(t, h, "", "/static/roboto-flex-OFL.txt"); rec.Code != http.StatusOK {
		t.Error("la licence de la police doit accompagner le fichier")
	}
}

func TestIndicateurDuProchainReleve(t *testing.T) {
	now := time.Now()
	p := nextReading(now.Add(-4*time.Minute), now, 10*time.Minute)
	if p == nil {
		t.Fatal("aucun indicateur")
	}
	if !strings.Contains(p.Label, "6") || !strings.Contains(string(p.SVG), `aria-valuenow="40"`) {
		t.Errorf("indicateur inattendu : %q %s", p.Label, p.SVG)
	}
	if nextReading(now.Add(-time.Hour), now, 10*time.Minute) != nil {
		t.Error("un releve en retard ne doit pas afficher d'attente")
	}
	if nextReading(time.Time{}, now, 10*time.Minute) != nil {
		t.Error("sans releve, pas d'indicateur")
	}
}

func TestLaVueDEnsembleSeRechargeALArriveeDuReleve(t *testing.T) {
	h, token := serveurAvec(t, 3, essai{Collecting: true, Interval: 10 * time.Minute})
	if body := get(t, h, token, "/").Body.String(); !strings.Contains(body, `http-equiv="refresh"`) {
		t.Error("la page ne se recharge pas pendant la collecte")
	}
	h, token = serveurAvec(t, 3, essai{Collecting: false})
	if body := get(t, h, token, "/").Body.String(); strings.Contains(body, `http-equiv="refresh"`) {
		t.Error("sans collecte, la page n'a pas a se recharger")
	}
}

func TestAucunStyleEnLigne(t *testing.T) {
	// La CSP n'autorise que la feuille de style : un attribut style serait
	// ignore par le navigateur, et la mise en page casserait sans bruit.
	h, token := serveurAvec(t, 5, essai{Collecting: true})
	for _, url := range []string{"/", "/descendant/1", "/montant/5", "/connexion", "/introuvable"} {
		if strings.Contains(get(t, h, token, url).Body.String(), "style=") {
			t.Errorf("%s contient un style en ligne", url)
		}
	}
}

func TestMireEstInstallable(t *testing.T) {
	h, token := serveurAvec(t, 1, essai{})

	// Le navigateur lit le manifeste sans cookie : il doit se servir sans session.
	rec := get(t, h, "", "/manifest.webmanifest")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/manifest+json" {
		t.Fatalf("manifeste : HTTP %d, %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	var m manifest
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.Name == "" || m.StartURL != "/" || m.Display != "standalone" {
		t.Errorf("manifeste incomplet : %+v", m)
	}
	tailles := map[string]bool{}
	for _, ic := range m.Icons {
		img := get(t, h, "", ic.Src)
		if img.Code != http.StatusOK {
			t.Errorf("icone %s : HTTP %d", ic.Src, img.Code)
			continue
		}
		cfg, err := png.DecodeConfig(img.Body)
		if err != nil {
			t.Errorf("icone %s illisible : %v", ic.Src, err)
			continue
		}
		if got := fmt.Sprintf("%dx%d", cfg.Width, cfg.Height); got != ic.Sizes {
			t.Errorf("icone %s : %s annonce, %s reel", ic.Src, ic.Sizes, got)
		}
		tailles[ic.Sizes+" "+ic.Purpose] = true
	}
	for _, requis := range []string{"192x192 any", "512x512 any", "512x512 maskable"} {
		if !tailles[requis] {
			t.Errorf("icone %s manquante", requis)
		}
	}
	if rec := get(t, h, "", "/static/icone-apple-180.png"); rec.Code != http.StatusOK {
		t.Errorf("icone iOS : HTTP %d", rec.Code)
	}

	page := get(t, h, token, "/")
	body := page.Body.String()
	for _, attendu := range []string{`rel="manifest" href="/manifest.webmanifest"`, `rel="apple-touch-icon"`, `name="theme-color"`} {
		if !strings.Contains(body, attendu) {
			t.Errorf("page sans %s", attendu)
		}
	}
	if csp := page.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "manifest-src 'self'") {
		t.Errorf("la CSP bloque le manifeste : %q", csp)
	}
}

func TestStylesDIcone(t *testing.T) {
	for _, o := range settings.AppIcons() {
		db := baseRemplie(t, 1)
		if err := settings.Set(context.Background(), db, settings.Source{}, map[string]string{settings.AppIcon: o.Value}); err != nil {
			t.Fatal(err)
		}
		h, _ := monter(t, db, essai{})
		tok := session(t, db)
		var m manifest
		if err := json.Unmarshal(get(t, h, "", "/manifest.webmanifest").Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		pre := iconPrefix(o.Value)
		vus := map[string]bool{}
		for _, ic := range m.Icons {
			img := get(t, h, "", ic.Src)
			cfg, err := png.DecodeConfig(img.Body)
			if img.Code != http.StatusOK || err != nil || fmt.Sprintf("%dx%d", cfg.Width, cfg.Height) != ic.Sizes {
				t.Errorf("%s : icône %s absente ou de mauvaise taille", o.Value, ic.Src)
			}
			vus[ic.Purpose] = true
			if ic.Purpose != "monochrome" && !strings.HasPrefix(ic.Src, pre) {
				t.Errorf("%s : %s n'est pas du style choisi", o.Value, ic.Src)
			}
		}
		if !vus["any"] || !vus["maskable"] || !vus["monochrome"] {
			t.Errorf("%s : %v", o.Value, vus)
		}
		apple := pre + "apple-180.png"
		if !strings.Contains(get(t, h, tok, "/").Body.String(), `rel="apple-touch-icon" href="`+apple+`"`) {
			t.Errorf("%s : icône iOS absente de la page", o.Value)
		}
		if rec := get(t, h, "", apple); rec.Code != http.StatusOK {
			t.Errorf("%s : %s HTTP %d", o.Value, apple, rec.Code)
		}
		body := get(t, h, tok, "/reglages/application").Body.String()
		if strings.Count(body, `type="radio" name="application.icone"`) != 4 ||
			!strings.Contains(body, `value="`+o.Value+`" checked><img src="`+pre+`192.png"`) {
			t.Errorf("%s : choix des styles absent ou mal coché", o.Value)
		}
	}
	// Un style inconnu revient au classique, sans lien casse.
	if iconPrefix("inconnu") != "/static/icone-" || iconPrefix("classique") != "/static/icone-" {
		t.Error("style inconnu")
	}
}

func TestThemeImpose(t *testing.T) {
	for theme, voulu := range map[string][2]string{
		settings.ThemeAuto:  {`<html lang="fr">`, `<meta name="color-scheme" content="light dark">`},
		settings.ThemeLight: {`<html lang="fr" data-theme="clair">`, `<meta name="color-scheme" content="light">`},
		settings.ThemeDark:  {`<html lang="fr" data-theme="sombre">`, `<meta name="color-scheme" content="dark">`},
	} {
		db := baseRemplie(t, 1)
		if err := settings.Set(context.Background(), db, settings.Source{}, map[string]string{settings.AppTheme: theme}); err != nil {
			t.Fatal(err)
		}
		h, _ := monter(t, db, essai{})
		tok := session(t, db)
		// Toutes les pages, connexion comprise, suivent le theme choisi.
		for _, page := range []string{"/", "/connexion"} {
			body := get(t, h, tok, page).Body.String()
			if page == "/connexion" {
				body = get(t, h, "", page).Body.String()
			}
			for _, v := range voulu {
				if !strings.Contains(body, v) {
					t.Errorf("%s, %s : %s absent", theme, page, v)
				}
			}
		}
	}
}
