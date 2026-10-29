package webview

import (
	"context"
	"database/sql"
	"html"
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/probe"
	"github.com/floopich/mire/internal/settings"
	"github.com/floopich/mire/internal/store"
)

func TestPageInternet(t *testing.T) {
	db := baseRemplie(t, 3)
	ctx := context.Background()
	now := time.Now().UTC()
	rtt := 12.0
	for i := 6; i >= 1; i-- {
		s := store.ProbeSample{Start: now.Add(-time.Duration(i) * 5 * time.Minute).Truncate(5 * time.Minute), Rounds: 10, RTTMin: &rtt, RTTAvg: &rtt, RTTMax: &rtt}
		if i == 3 {
			s.Failed, s.RTTMin, s.RTTAvg, s.RTTMax = 10, nil, nil, nil
		}
		if err := store.SaveProbeSample(ctx, db, s); err != nil {
			t.Fatal(err)
		}
	}
	id, err := store.OpenOutage(ctx, db, probe.KindLine, now.Add(-16*time.Minute), "Coupure de la ligne : Internet ne répond plus, le modem si.")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CloseOutage(ctx, db, id, now.Add(-10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	h, _ := monter(t, db, essai{Collecting: true})
	tok := session(t, db)

	body := html.UnescapeString(get(t, h, tok, "/internet").Body.String())
	for _, attendu := range []string{"Ligne coupée", "6 min", "12 ms", "16,7 %", `class="chart wide"`, "Ligne coupée</b> pendant 6\u00a0min", `class="bar-noncorr"`} {
		if !strings.Contains(body, attendu) {
			t.Errorf("page Internet sans %q", attendu)
		}
	}
	// La sonde du faux service est arretee : la page le dit.
	if !strings.Contains(body, "Sonde désactivée") {
		t.Error("sonde arretee non signalee")
	}

	// Une coupure en cours s'annonce sur la vue d'ensemble.
	if _, err := store.OpenOutage(ctx, db, probe.KindLine, now.Add(-3*time.Minute), "Coupure de la ligne : Internet ne répond plus, le modem si."); err != nil {
		t.Fatal(err)
	}
	index := html.UnescapeString(get(t, h, tok, "/").Body.String())
	if !strings.Contains(index, "Internet ne répond plus, le modem si. Depuis 3 min.") || !strings.Contains(index, `href="/internet"`) {
		t.Error("coupure en cours absente de la vue d'ensemble")
	}
}

func TestPageInternetSansMesure(t *testing.T) {
	h, token := serveurAvec(t, 1, essai{})
	if body := get(t, h, token, "/internet").Body.String(); !strings.Contains(body, "Aucune mesure sur cette période") {
		t.Error("page vide sans explication")
	}
}

func TestDesPertesFaiblesRestentLisibles(t *testing.T) {
	to := time.Unix(1_700_000_000, 0)
	from := to.Add(-time.Hour)
	svg := LossBars("p", []store.ProbeSample{{Start: to.Add(-30 * time.Minute), Rounds: 400, Failed: 1}}, from, to)
	// 0,25 % au plus : l'echelle s'arrondit a 0,4 %, avec un repere a
	// 0,2 %, plutot qu'un « 0 % » trompeur.
	if !strings.Contains(svg, "0,2\u202f%") || !strings.Contains(svg, "0,4\u202f%") {
		t.Errorf("repere maximal illisible : %s", svg)
	}
	if svg := LossBars("p", []store.ProbeSample{{Start: to.Add(-30 * time.Minute), Rounds: 10}}, from, to); !strings.Contains(svg, "Toutes les tentatives") {
		t.Error("sans perte, le graphique doit le dire")
	}
}

func TestCompensationLegaleBelge(t *testing.T) {
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Second)
	coupure := func(db *sql.DB, kind string, start time.Time, d time.Duration) {
		t.Helper()
		id, err := store.OpenOutage(ctx, db, kind, start, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CloseOutage(ctx, db, id, start.Add(d)); err != nil {
			t.Fatal(err)
		}
	}

	db := baseRemplie(t, 1)
	coupure(db, probe.KindLine, now.Add(-40*time.Hour), 30*time.Hour)  // 2 € au bareme
	coupure(db, probe.KindLine, now.Add(-6*time.Hour), 2*time.Hour)    // trop courte
	coupure(db, probe.KindLocal, now.Add(-70*time.Hour), 20*time.Hour) // reseau local : exclue
	h, _ := monter(t, db, essai{})
	body := get(t, h, session(t, db), "/internet?heures=168").Body.String()
	if strings.Count(body, `class="pastille"`) != 1 || !strings.Contains(body, "Compensation estimée : 2,00"+nbsp+"€") {
		t.Errorf("estimation absente ou en trop")
	}
	if !strings.Contains(body, "Une coupure dépasse 8 heures : environ 2,00"+nbsp+"€") {
		t.Error("le total n'est pas annonce")
	}

	// Avec le prix de l'abonnement, le plancher d'un trentieme par jour
	// entame l'emporte : 90 euros par mois, deux jours entames, 6 euros.
	db = baseRemplie(t, 1)
	coupure(db, probe.KindLine, now.Add(-40*time.Hour), 30*time.Hour)
	h, _ = monter(t, db, essai{Forced: settings.Source{settings.MonthlyFee: "90"}})
	if body := get(t, h, session(t, db), "/internet?heures=168").Body.String(); !strings.Contains(body, "6,00"+nbsp+"€") {
		t.Error("le plancher de l'abonnement n'est pas applique")
	}

	// Hors de Belgique, rien.
	db = baseRemplie(t, 1)
	coupure(db, probe.KindLine, now.Add(-40*time.Hour), 30*time.Hour)
	h, _ = monter(t, db, essai{Forced: settings.Source{settings.Profile: "generique"}})
	if body := get(t, h, session(t, db), "/internet?heures=168").Body.String(); strings.Contains(body, "Compensation") {
		t.Error("la compensation belge ne concerne qu'un operateur belge")
	}
}
