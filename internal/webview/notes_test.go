package webview

import (
	"bytes"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/store"
)

func TestNotesDuJournal(t *testing.T) {
	db := baseRemplie(t, 1)
	h, _ := monter(t, db, essai{})
	token := session(t, db)
	quand := time.Now().Add(-time.Hour).Format(noteTimeLayout)

	rec := posteAvec(t, h, token, "/evenements/notes?heures=168", url.Values{"texte": {"Technicien VOO passé, connecteur refait"}, "quand": {quand}})
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/evenements?heures=168") {
		t.Fatalf("ajout : HTTP %d vers %q", rec.Code, rec.Header().Get("Location"))
	}
	body := get(t, h, token, "/evenements").Body.String()
	if !strings.Contains(body, "<b>Note.</b> Technicien VOO passé, connecteur refait") {
		t.Fatal("la note n'apparait pas dans le journal")
	}

	refus := map[string]url.Values{
		"vide":   {"texte": {"  "}, "quand": {quand}},
		"longue": {"texte": {strings.Repeat("é", store.NoteMaxLength+1)}, "quand": {quand}},
		"futur":  {"texte": {"demain"}, "quand": {time.Now().Add(48 * time.Hour).Format(noteTimeLayout)}},
		"date":   {"texte": {"sans date"}, "quand": {"hier"}},
	}
	for nom, form := range refus {
		if rec := posteAvec(t, h, token, "/evenements/notes", form); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("note %s : HTTP %d", nom, rec.Code)
		}
	}
	// Une note refusee est rendue pour etre corrigee, pas perdue.
	if rec := posteAvec(t, h, token, "/evenements/notes", refus["futur"]); !strings.Contains(rec.Body.String(), ">demain</textarea>") {
		t.Error("la saisie refusee est perdue")
	}

	list, _ := store.Events(t.Context(), db, store.EventFilter{From: time.Now().Add(-24 * time.Hour), To: time.Now()})
	var id string
	for _, e := range list {
		if e.Kind == store.NoteKind {
			id = strconv.FormatInt(e.ID, 10)
		}
	}
	if !strings.Contains(body, `form="note-`+id+`"`) || !strings.Contains(body, `<form id="note-`+id+`"`) {
		t.Error("la note n'a pas de bouton de suppression")
	}
	if rec := posteAvec(t, h, token, "/evenements/notes/"+id+"/supprimer", nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("suppression : HTTP %d", rec.Code)
	}
	if strings.Contains(get(t, h, token, "/evenements").Body.String(), "connecteur refait") {
		t.Error("la note supprimee est toujours la")
	}
	if rec := posteAvec(t, h, token, "/evenements/notes/"+id+"/supprimer", nil); rec.Code != http.StatusNotFound {
		t.Errorf("seconde suppression : HTTP %d", rec.Code)
	}
}

func TestSauvegardeTelechargee(t *testing.T) {
	db := baseRemplie(t, 3)
	h, _ := monter(t, db, essai{})
	token := session(t, db)
	if rec := get(t, h, "", "/reglages/sauvegarde/telecharger"); rec.Code != http.StatusSeeOther {
		t.Errorf("sans session : HTTP %d", rec.Code)
	}
	rec := get(t, h, token, "/reglages/sauvegarde/telecharger")
	if rec.Code != http.StatusOK || !bytes.HasPrefix(rec.Body.Bytes(), []byte("SQLite format 3\x00")) {
		t.Fatalf("HTTP %d, %d octets", rec.Code, rec.Body.Len())
	}
	if d := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(d, `attachment; filename="mire-`) {
		t.Errorf("Content-Disposition %q", d)
	}
}

func TestLaSauvegardeNeLaissePasDeFichier(t *testing.T) {
	db := baseRemplie(t, 1)
	dir := t.TempDir()
	h, _ := monter(t, db, essai{DataDir: dir})
	if rec := get(t, h, session(t, db), "/reglages/sauvegarde/telecharger"); rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d", rec.Code)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("fichiers restants : %v", entries)
	}
}
