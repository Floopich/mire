package webview

import (
	"context"
	"html"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/store"
)

func TestJournalDesEvenements(t *testing.T) {
	db := baseRemplie(t, 3)
	now := time.Now()
	err := store.SaveEvents(context.Background(), db, []store.Event{
		{Time: now.Add(-50 * time.Hour), Kind: "etat", Level: 3, Message: "Ligne à surveiller : canal descendant 1."},
		{Time: now.Add(-30 * time.Minute), Kind: "canaux_perdus", Level: 3, Direction: "descendant", ChannelID: 1, Message: "Canal descendant 1 perdu."},
		{Time: now.Add(-40 * 24 * time.Hour), Kind: "etat", Level: 1, Message: "Très ancien."},
	})
	if err != nil {
		t.Fatal(err)
	}
	h, _ := monter(t, db, essai{Collecting: true})
	tok := session(t, db)

	body := html.UnescapeString(get(t, h, tok, "/evenements").Body.String())
	// Il y a 30 minutes : aujourd'hui, ou hier juste apres minuit.
	if !strings.Contains(body, "Aujourd'hui") && !strings.Contains(body, "Hier") {
		t.Error("journal sans le nom du jour")
	}
	for _, attendu := range []string{"Canal descendant 1 perdu.", `href="/descendant/1?heures=168"`, "Ligne à surveiller", "2 événements"} {
		if !strings.Contains(body, attendu) {
			t.Errorf("journal sans %q", attendu)
		}
	}
	if strings.Contains(body, "Très ancien") {
		t.Error("la fenetre par defaut d'une semaine n'est pas respectee")
	}
	if !strings.Contains(get(t, h, tok, "/evenements?heures=2160").Body.String(), "Très ancien") {
		t.Error("une fenetre elargie doit remonter plus loin")
	}

	// La vue d'ensemble reprend les plus recents, la page du canal les siens.
	if body := get(t, h, tok, "/").Body.String(); !strings.Contains(body, "Canal descendant 1 perdu.") || !strings.Contains(body, `href="/evenements"`) {
		t.Error("vue d'ensemble sans les derniers evenements")
	}
	canal := get(t, h, tok, "/descendant/1").Body.String()
	if !strings.Contains(canal, "Canal descendant 1 perdu.") || strings.Contains(canal, "Ligne à surveiller") {
		t.Error("la page du canal doit lister ses evenements, et seulement les siens")
	}
	if get(t, h, "", "/evenements").Code != http.StatusSeeOther {
		t.Error("le journal doit exiger une session")
	}
}

func TestJournalVide(t *testing.T) {
	h, token := serveurAvec(t, 3, essai{})
	if body := get(t, h, token, "/evenements").Body.String(); !strings.Contains(body, "Aucun événement sur cette période") {
		t.Error("journal vide sans explication")
	}
}

func TestNomDesJours(t *testing.T) {
	now := time.Date(2026, 10, 25, 22, 0, 0, 0, time.Local)
	for at, want := range map[time.Time]string{
		time.Date(2026, 10, 25, 1, 0, 0, 0, time.Local):  "Aujourd'hui",
		time.Date(2026, 10, 24, 23, 0, 0, 0, time.Local): "Hier",
		time.Date(2026, 10, 20, 9, 0, 0, 0, time.Local):  "mardi 20 octobre",
		time.Date(2025, 8, 1, 9, 0, 0, 0, time.Local):    "vendredi 1 août 2025",
	} {
		if got := DayLabel(at, now); got != want {
			t.Errorf("%v : %q, attendu %q", at, got, want)
		}
	}
}

func TestGraphiqueDesErreurs(t *testing.T) {
	to := time.Unix(1_700_000_000, 0)
	from := to.Add(-24 * time.Hour)
	if svg := ErrorBars("e", nil, from, to); !strings.Contains(svg, "Pas encore assez") {
		t.Error("sans releve, le graphique doit le dire")
	}
	calme := []store.ErrorPoint{{Timestamp: to.Add(-time.Hour)}}
	if svg := ErrorBars("e", calme, from, to); !strings.Contains(svg, "Aucune erreur") {
		t.Error("sans erreur, le graphique doit le dire")
	}
	pts := []store.ErrorPoint{
		{Timestamp: to.Add(-2 * time.Hour), Corrected: 1200, Uncorrected: 30},
		{Timestamp: to, Corrected: 10},
	}
	svg := ErrorBars("e", pts, from, to)
	if strings.Count(svg, `class="bar-noncorr"`) != 2 || strings.Count(svg, `class="bar-corr"`) != 4 {
		t.Errorf("barres inattendues : %s", svg)
	}
	// 1 230 erreurs au plus : l'echelle s'arrondit a 2 000, abrege.
	if !strings.Contains(svg, ">2"+nbsp+"k<") || !strings.Contains(svg, ">1"+nbsp+"k<") {
		t.Errorf("les reperes doivent etre ronds et abreges : %s", svg)
	}
}
