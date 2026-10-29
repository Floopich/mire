package webview

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/store"
)

// eventRow est une entree du journal telle qu'affichee.
type eventRow struct {
	Time    time.Time
	Level   analyze.Level
	Message string
	// Href mene au canal en cause, vide si l'evenement en concerne plusieurs.
	Href string
	// Note marque une note de l'utilisateur ; Deletable, une note que la
	// page permet de supprimer.
	ID        int64
	Note      bool
	Deletable bool
}

// eventDay regroupe les entrees d'une meme journee.
type eventDay struct {
	Label  string
	Events []eventRow
}

type eventsView struct {
	common
	Days  []eventDay
	Count int
	Notes []eventRow
	// NoteNow preremplit la date d'une nouvelle note, au format du champ.
	NoteNow string
	NoteErr string
	Texte   string
}

// journalHours est la fenetre par defaut du journal : une semaine montre les
// motifs qui reviennent chaque soir sans noyer la page.
const journalHours = 7 * 24

func toRows(list []store.Event, hours int) []eventRow {
	out := make([]eventRow, 0, len(list))
	for _, e := range list {
		row := eventRow{Time: e.Time, Level: analyze.Level(e.Level), Message: e.Message, ID: e.ID, Note: e.Kind == store.NoteKind}
		if e.ChannelID != 0 && (e.Direction == "descendant" || e.Direction == "montant") {
			row.Href = "/" + e.Direction + "/" + strconv.Itoa(e.ChannelID) + "?heures=" + strconv.Itoa(hours)
		}
		out = append(out, row)
	}
	return out
}

// byDay regroupe des entrees deja triees de la plus recente a la plus ancienne.
func byDay(rows []eventRow, now time.Time) []eventDay {
	var out []eventDay
	for _, r := range rows {
		label := DayLabel(r.Time, now)
		if len(out) == 0 || out[len(out)-1].Label != label {
			out = append(out, eventDay{Label: label})
		}
		out[len(out)-1].Events = append(out[len(out)-1].Events, r)
	}
	return out
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	s.renderEvents(w, r, http.StatusOK, "", "")
}

// renderEvents affiche le journal ; noteErr et texte reprennent une note
// refusee pour qu'elle soit corrigee plutot que perdue.
func (s *Server) renderEvents(w http.ResponseWriter, r *http.Request, status int, noteErr, texte string) {
	v := eventsView{common: s.commonFor(r, "Événements"), NoteErr: noteErr, Texte: texte}
	if r.URL.Query().Get("heures") == "" {
		v.Hours = journalHours
		v.Windows = windowsFor(v.Hours)
	}
	now := s.opts.Now()
	list, err := store.Events(r.Context(), s.db, store.EventFilter{
		From: now.Add(-time.Duration(v.Hours) * time.Hour), To: now, Limit: 1000})
	if err != nil {
		s.fail(w, err)
		return
	}
	v.Count = len(list)
	rows := toRows(list, v.Hours)
	for i := range rows {
		if rows[i].Note {
			rows[i].Deletable = true
			v.Notes = append(v.Notes, rows[i])
		}
	}
	v.Days = byDay(rows, now)
	v.NoteNow = now.In(time.Local).Format(noteTimeLayout)
	s.render(w, status, "evenements", v)
}

// noteTimeLayout est le format d'un champ datetime-local.
const noteTimeLayout = "2006-01-02T15:04"

// handleAddNote inscrit une note au journal.
func (s *Server) handleAddNote(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "origine refusée", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "formulaire illisible", http.StatusBadRequest)
		return
	}
	texte := strings.TrimSpace(r.PostForm.Get("texte"))
	switch {
	case texte == "":
		s.renderEvents(w, r, http.StatusUnprocessableEntity, "La note est vide.", texte)
		return
	case utf8.RuneCountInString(texte) > store.NoteMaxLength:
		s.renderEvents(w, r, http.StatusUnprocessableEntity,
			fmt.Sprintf("La note dépasse %d caractères.", store.NoteMaxLength), texte)
		return
	}
	at, err := time.ParseInLocation(noteTimeLayout, r.PostForm.Get("quand"), time.Local)
	if err != nil {
		s.renderEvents(w, r, http.StatusUnprocessableEntity, "La date de la note est illisible.", texte)
		return
	}
	if at.After(s.opts.Now().Add(time.Minute)) {
		s.renderEvents(w, r, http.StatusUnprocessableEntity, "Une note ne peut pas être datée dans le futur.", texte)
		return
	}
	if err := store.AddNote(r.Context(), s.db, at, texte); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/evenements?heures="+strconv.Itoa(hoursParam(r))+"#journal", http.StatusSeeOther)
}

// handleDeleteNote retire une note du journal.
func (s *Server) handleDeleteNote(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "origine refusée", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.renderError(w, http.StatusNotFound, "Note introuvable", "Cette note n'existe pas.")
		return
	}
	ok, err := store.DeleteNote(r.Context(), s.db, id)
	if err != nil {
		s.fail(w, err)
		return
	}
	if !ok {
		s.renderError(w, http.StatusNotFound, "Note introuvable", "Cette note n'existe pas, ou a déjà été supprimée.")
		return
	}
	http.Redirect(w, r, "/evenements?heures="+strconv.Itoa(hoursParam(r))+"#journal", http.StatusSeeOther)
}

var (
	jours = [...]string{"dimanche", "lundi", "mardi", "mercredi", "jeudi", "vendredi", "samedi"}
	mois  = [...]string{"janvier", "février", "mars", "avril", "mai", "juin", "juillet",
		"août", "septembre", "octobre", "novembre", "décembre"}
)

// DayLabel nomme une journee : « Aujourd'hui », « Hier », « lundi 20 octobre »,
// avec l'annee quand ce n'est pas l'annee en cours.
func DayLabel(t, now time.Time) string {
	t, now = t.Local(), now.Local()
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	today := time.Date(y2, m2, d2, 0, 0, 0, 0, time.Local)
	day := time.Date(y1, m1, d1, 0, 0, 0, 0, time.Local)
	switch {
	case day.Equal(today):
		return "Aujourd'hui"
	case day.Equal(today.AddDate(0, 0, -1)):
		return "Hier"
	}
	label := jours[t.Weekday()] + " " + strconv.Itoa(d1) + " " + mois[m1-1]
	if y1 != y2 {
		label += " " + strconv.Itoa(y1)
	}
	return label
}
