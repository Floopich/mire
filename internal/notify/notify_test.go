package notify

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/events"
	"github.com/floopich/mire/internal/store"
)

func base(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// boite retient les messages envoyes ; fail fait echouer les envois.
type boite struct {
	mu   sync.Mutex
	recu []Message
	fail bool
}

func (b *boite) Send(_ context.Context, m Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fail {
		return errors.New("service indisponible")
	}
	b.recu = append(b.recu, m)
	return nil
}

func inscrire(t *testing.T, db *sql.DB, evs ...store.Event) {
	t.Helper()
	if err := store.SaveEvents(context.Background(), db, evs); err != nil {
		t.Fatal(err)
	}
}

func TestFiltres(t *testing.T) {
	etat := store.Event{Kind: events.KindState, Level: int(analyze.Warning)}
	modulation := store.Event{Kind: events.KindModulation, Level: int(analyze.Warning)}
	critique := store.Event{Kind: events.KindModulation, Level: int(analyze.Critical)}
	cas := []struct {
		e      store.Event
		filtre string
		voulu  bool
	}{
		{etat, FilterState, true},
		{modulation, FilterState, false},
		{modulation, FilterAll, true},
		{critique, FilterCritical, true},
		{etat, FilterCritical, false},
		{etat, "", true}, // sans choix, le filtre « etat » s'applique
		{store.Event{Kind: store.NoteKind}, FilterAll, false},
	}
	for _, c := range cas {
		if got := Wanted(c.e, c.filtre); got != c.voulu {
			t.Errorf("%s, filtre %q : %v, attendu %v", c.e.Kind, c.filtre, got, c.voulu)
		}
	}
}

func TestComposition(t *testing.T) {
	at := time.Date(2026, 10, 26, 19, 30, 0, 0, time.UTC)
	seul := Compose([]store.Event{{Time: at, Kind: events.KindOutage, Level: int(analyze.Critical), Message: "Coupure d'Internet."}},
		"https://mire.exemple.be/")
	if seul.Title != "Mire : alerte critique" || seul.Body != "Coupure d'Internet." {
		t.Errorf("message unique : %+v", seul)
	}
	if seul.Link != "https://mire.exemple.be/evenements" {
		t.Errorf("lien %q", seul.Link)
	}

	var plusieurs []store.Event
	for i := range 12 {
		plusieurs = append(plusieurs, store.Event{Time: at.Add(time.Duration(i) * time.Minute), Kind: events.KindState,
			Level: int(analyze.Warning), Message: "Ligne à surveiller."})
	}
	groupe := Compose(plusieurs, "")
	if groupe.Link != "" {
		t.Error("sans adresse de Mire, aucun lien")
	}
	lignes := strings.Split(groupe.Body, "\n")
	if len(lignes) != maxLines+1 || !strings.Contains(lignes[maxLines], "4 autres") {
		t.Errorf("message groupe mal borne :\n%s", groupe.Body)
	}
	if !groupe.Time.Equal(at.Add(11 * time.Minute)) {
		t.Errorf("heure %v, attendu celle du dernier evenement", groupe.Time)
	}
}

func TestLeJournalEstEnvoyeSansPerteNiDoublon(t *testing.T) {
	db := base(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	inscrire(t, db, store.Event{Time: now.Add(-time.Minute), Kind: events.KindOutage, Level: int(analyze.Critical), Message: "ancienne"})

	b := &boite{}
	n := &Notifier{DB: db, Sender: b, Filter: FilterState, Now: func() time.Time { return now }}

	// A la mise en service, le passe n'est pas envoye.
	n.Poll(ctx)
	if len(b.recu) != 0 {
		t.Fatalf("%d messages envoyes pour le passe", len(b.recu))
	}

	inscrire(t, db,
		store.Event{Time: now, Kind: events.KindOutage, Level: int(analyze.Critical), Message: "Coupure."},
		store.Event{Time: now, Kind: events.KindModulation, Level: int(analyze.Warning), Message: "filtree"},
	)
	b.fail = true
	n.Poll(ctx)
	b.fail = false
	n.Poll(ctx) // l'echec precedent est retente
	n.Poll(ctx) // et rien n'est renvoye ensuite
	if len(b.recu) != 1 || b.recu[0].Body != "Coupure." {
		t.Fatalf("messages recus : %+v", b.recu)
	}

	// Un evenement trop ancien (instance arretee longtemps) n'est plus envoye.
	inscrire(t, db, store.Event{Time: now.Add(-2 * time.Hour), Kind: events.KindOutage, Level: int(analyze.Critical), Message: "perime"})
	n.Poll(ctx)
	if len(b.recu) != 1 {
		t.Errorf("un evenement perime a ete envoye : %+v", b.recu[len(b.recu)-1])
	}
}

func TestEssai(t *testing.T) {
	b := &boite{}
	if err := Test(context.Background(), b, ""); err != nil {
		t.Fatal(err)
	}
	if len(b.recu) != 1 || b.recu[0].Title != "Mire : essai" {
		t.Errorf("essai : %+v", b.recu)
	}
}
