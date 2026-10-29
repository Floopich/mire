package webview

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/events"
	"github.com/floopich/mire/internal/store"
)

// journalRapport ajoute a baseJours un episode « a surveiller », une
// coupure, un redemarrage et une note.
func journalRapport(t *testing.T, db *sql.DB, now time.Time) {
	t.Helper()
	ctx := context.Background()
	day := func(d, h int) time.Time {
		return time.Date(now.Year(), now.Month(), now.Day(), h, 0, 0, 0, time.Local).AddDate(0, 0, d)
	}
	if err := store.SaveEvents(ctx, db, []store.Event{
		{Time: day(-10, 6), Kind: events.KindState, Level: int(analyze.Warning), Message: "Ligne à surveiller : canal 3."},
		{Time: day(-8, 6), Kind: events.KindState, Level: int(analyze.Good), Message: "Ligne revenue en bon état."},
		{Time: day(-9, 3), Kind: events.KindRestart, Level: int(analyze.Warning), Message: "Le modem a redémarré."},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddNote(ctx, db, day(-9, 10), "Technicien passé : connecteur refait."); err != nil {
		t.Fatal(err)
	}
	for _, o := range []struct {
		kind       string
		start, end time.Time
	}{
		{"ligne", day(-12, 20), day(-12, 22)},
		{"ligne", day(-6, 9), day(-6, 9).Add(30 * time.Minute)},
		{"local", day(-4, 1), day(-4, 4)},
	} {
		id, err := store.OpenOutage(ctx, db, o.kind, o.start, "Coupure.")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CloseOutage(ctx, db, id, o.end); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRapport(t *testing.T) {
	now := time.Now()
	db := baseJours(t, now)
	journalRapport(t, db, now)
	h, _ := monter(t, db, essai{Collecting: true})
	cookie := session(t, db)

	du := now.AddDate(0, 0, -19).Format(time.DateOnly)
	au := now.Format(time.DateOnly)
	interv := now.AddDate(0, 0, -9).Format(time.DateOnly)
	rec := get(t, h, cookie, "/rapport?du="+du+"&au="+au+"&intervention="+interv+"&jours=7")
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d", rec.Code)
	}
	body := rec.Body.String()
	for _, voulu := range []string{
		"la ligne a été en bon état ",
		" du temps et à surveiller 1",
		"Aucune erreur non corrigée sur la période.",
		"Internet a été coupé 2 fois, 2 h 30 au total ; la plus longue a duré 2 h, le ",
		"Le réseau local a été coupé 1 fois, 3 h au total",
		"Le modem a redémarré 1 fois.",
		// L'OFDM publie 26,6 dBmV a chaque releve : toujours hors plage.
		"Canaux descendants hors plage (part du temps) : 159 (" + percent(100, 0) + ").",
		"Technicien passé : connecteur refait.",
		"Ligne à surveiller : canal 3.",
		"Avant et après l'intervention",
		"7 jours avant",
		`<th scope="row">Temps en bon état</th>`,
	} {
		if !strings.Contains(body, voulu) {
			t.Errorf("absent : %s", voulu)
		}
	}
	// Les retours a la normale ne figurent pas au journal du rapport.
	if strings.Contains(body, "Ligne revenue en bon état.") {
		t.Error("un retour à la normale figure au journal")
	}

	// Saisies refusees : le rapport par defaut s'affiche avec l'erreur.
	for q, msg := range map[string]string{
		"?du=2026-10-20&au=2026-10-01": "La date de début doit précéder la date de fin.",
		"?du=2020-01-01&au=2026-01-01": "La période dépasse un an",
		"?du=demain":                   "Date de début illisible.",
		"?intervention=" + now.AddDate(0, 0, 3).Format(time.DateOnly): "ne peut pas être dans le futur",
	} {
		if b := get(t, h, cookie, "/rapport"+q).Body.String(); !strings.Contains(b, msg) {
			t.Errorf("%s : message %q absent", q, msg)
		}
	}
	if !strings.Contains(get(t, h, cookie, "/").Body.String(), `href="/rapport"`) {
		t.Error("aucun lien depuis la vue d'ensemble")
	}
}

func TestDonneesDeLaPeriode(t *testing.T) {
	now := time.Now()
	db := baseJours(t, now)
	journalRapport(t, db, now)
	h, _ := monter(t, db, essai{Collecting: true})
	cookie := session(t, db)

	du := now.AddDate(0, 0, -19).Format(time.DateOnly)
	au := now.Format(time.DateOnly)
	body := get(t, h, cookie, "/rapport?du="+du+"&au="+au).Body.String()
	for _, voulu := range []string{"Données de la période", `/rapport/donnees/descendant.csv?au=` + au + `&amp;du=` + du,
		"/rapport/donnees/coupures.csv?", "/rapport/donnees/journal.csv?"} {
		if !strings.Contains(body, voulu) {
			t.Errorf("absent : %s", voulu)
		}
	}
	// Sans mesure de debit, pas de tableau vide a telecharger.
	if strings.Contains(body, "/rapport/donnees/debit.csv") {
		t.Error("tableau du débit proposé sans mesure")
	}

	rec := get(t, h, cookie, "/rapport/donnees/coupures.csv?du="+du+"&au="+au)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/csv; charset=utf-8" ||
		!strings.Contains(rec.Header().Get("Content-Disposition"), `filename="mire-coupures-`+du+"-"+au+`.csv"`) {
		t.Fatalf("HTTP %d, %v", rec.Code, rec.Header())
	}
	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if len(lines) != 4 || lines[0] != "debut,fin,duree_s,cause,detail" || !strings.HasSuffix(lines[1], ",7200,ligne,Coupure.") {
		t.Errorf("coupures : %q", lines)
	}
	if rec := get(t, h, cookie, "/rapport/donnees/inconnu.csv"); rec.Code != http.StatusNotFound {
		t.Errorf("tableau inconnu : HTTP %d", rec.Code)
	}
	if rec := get(t, h, cookie, "/rapport/donnees/journal.csv?du=demain"); rec.Code != http.StatusBadRequest {
		t.Errorf("période illisible : HTTP %d", rec.Code)
	}
	if rec := get(t, h, "", "/rapport/donnees/journal.csv"); rec.Code != http.StatusSeeOther {
		t.Errorf("sans session : HTTP %d", rec.Code)
	}
}
