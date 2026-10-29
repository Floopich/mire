package store

import (
	"context"
	"testing"
	"time"

	"github.com/floopich/mire/internal/docsis"
)

// releveErreurs est un releve d'un canal descendant aux compteurs donnes.
func releveErreurs(at time.Time, id int, corr, noncorr int64) docsis.Snapshot {
	return docsis.Snapshot{Timestamp: at, Downstream: []docsis.Downstream{
		{ChannelID: id, Modulation: "256QAM", CorrErrors: corr, NonCorrErrors: noncorr},
	}}
}

func TestLesErreursSeComptentParAccroissement(t *testing.T) {
	db := baseTest(t)
	ctx := context.Background()
	t0 := time.Unix(1_700_000_000, 0).UTC()
	// Compteurs cumulatifs ; le modem redemarre entre le 4e et le 5e releve.
	serie := [][2]int64{{1000, 10}, {1100, 10}, {1300, 15}, {1300, 15}, {40, 2}, {90, 2}}
	for i, c := range serie {
		if _, err := SaveSnapshot(ctx, db, releveErreurs(t0.Add(time.Duration(i)*10*time.Minute), 1, c[0], c[1])); err != nil {
			t.Fatal(err)
		}
	}

	// Toute la serie : 100 + 200 + 0 + 40 (depuis le redemarrage) + 50.
	tot, err := ErrorTotals(ctx, db, t0, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got := tot[1]; got.Corrected != 390 || got.Uncorrected != 7 {
		t.Errorf("totaux %+v, attendu 390 corrigees et 7 non corrigees", got)
	}

	// Une fenetre qui commence entre deux releves repart du releve anterieur.
	tot, _ = ErrorTotals(ctx, db, t0.Add(25*time.Minute), t0.Add(time.Hour))
	if got := tot[1]; got.Corrected != 90 || got.Uncorrected != 2 {
		t.Errorf("fenetre partielle %+v, attendu 90 et 2", got)
	}

	hist, err := ErrorHistory(ctx, db, 1, t0, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 5 || hist[3].Corrected != 40 || hist[3].Uncorrected != 2 {
		t.Errorf("historique inattendu : %+v", hist)
	}
}

func TestUnSeulReleveNeDonneAucunCompte(t *testing.T) {
	db := baseTest(t)
	ctx := context.Background()
	t0 := time.Unix(1_700_000_000, 0).UTC()
	if _, err := SaveSnapshot(ctx, db, releveErreurs(t0, 1, 500, 50)); err != nil {
		t.Fatal(err)
	}
	tot, err := ErrorTotals(ctx, db, t0.Add(-time.Hour), t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := tot[1]; ok {
		t.Error("un canal vu une seule fois doit rester inconnu, pas compte sans erreur")
	}
}

func TestJournal(t *testing.T) {
	db := baseTest(t)
	ctx := context.Background()
	t0 := time.Unix(1_700_000_000, 0).UTC()
	err := SaveEvents(ctx, db, []Event{
		{Time: t0, Kind: "etat", Level: 3, Message: "Ligne à surveiller."},
		{Time: t0.Add(time.Hour), Kind: "canaux_perdus", Level: 3, Direction: "descendant", ChannelID: 7, Message: "Canal descendant 7 perdu."},
		{Time: t0.Add(2 * time.Hour), Kind: "etat", Level: 1, Message: "Ligne revenue en bon état."},
	})
	if err != nil {
		t.Fatal(err)
	}
	all, err := Events(ctx, db, EventFilter{From: t0, To: t0.Add(3 * time.Hour)})
	if err != nil || len(all) != 3 || all[0].Level != 1 {
		t.Fatalf("journal %+v (%v), attendu 3 entrees, la plus recente d'abord", all, err)
	}
	canal, _ := Events(ctx, db, EventFilter{From: t0, To: t0.Add(3 * time.Hour), Direction: "descendant", ChannelID: 7})
	if len(canal) != 1 || canal[0].ChannelID != 7 {
		t.Errorf("filtre par canal : %+v", canal)
	}
	last, ok, err := LastEvent(ctx, db, "etat")
	if err != nil || !ok || last.Message != "Ligne revenue en bon état." {
		t.Errorf("dernier etat : %+v %v %v", last, ok, err)
	}
	if _, ok, _ := LastEvent(ctx, db, "inexistant"); ok {
		t.Error("type absent trouve")
	}
}

func TestLeJournalSuitLaConservation(t *testing.T) {
	db := baseTest(t)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	// Sans releve, le journal n'est pas vide par la purge : il peut ne
	// contenir que des pannes de collecte.
	if err := SaveEvents(ctx, db, []Event{{Time: now.AddDate(0, 0, -2), Kind: "modem_injoignable", Message: "x"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Purge(ctx, db, Retention{}, now); err != nil {
		t.Fatal(err)
	}
	if list, _ := Events(ctx, db, EventFilter{From: time.Unix(0, 0), To: now}); len(list) != 1 {
		t.Fatal("la purge a vide un journal sans releve")
	}

	if _, err := SaveSnapshot(ctx, db, exemple(now.AddDate(0, 0, -1))); err != nil {
		t.Fatal(err)
	}
	if err := SaveEvents(ctx, db, []Event{{Time: now.AddDate(0, 0, -40), Kind: "etat", Message: "vieux"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Purge(ctx, db, Retention{Days: 30}, now); err != nil {
		t.Fatal(err)
	}
	list, _ := Events(ctx, db, EventFilter{From: time.Unix(0, 0), To: now})
	if len(list) != 0 {
		t.Errorf("%d evenements anterieurs au plus ancien releve conserves", len(list))
	}
}
