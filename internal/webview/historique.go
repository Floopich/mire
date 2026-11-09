package webview

import (
	"net/http"
	"time"

	"github.com/floopich/mire/internal/store"
)

// La page Historique rassemble ce qui regarde en arriere : la frise, le
// journal, la tendance et le rapport. Chaque page y est a un appui, toute
// page de Mire a deux appuis au plus depuis le menu.

type historyEntry struct {
	Href, Icon, Title, Detail string
}

type historyView struct {
	common
	Entries []historyEntry
}

// historyWeek est la periode resumee pour le journal.
const historyWeek = 7 * 24 * time.Hour

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := s.opts.Now()
	journal := "Aucun événement depuis 7 jours."
	list, err := store.Events(ctx, s.db, store.EventFilter{From: now.Add(-historyWeek), To: now, Limit: 1000})
	if err != nil {
		s.fail(w, err)
		return
	}
	if n := len(list); n > 0 {
		journal = plural(n, "événement", "événements") + " sur 7 jours, le dernier " + Ago(list[0].Time, now) + "."
	}
	outages, err := store.Outages(ctx, s.db, now.AddDate(0, 0, -30), now)
	if err != nil {
		s.fail(w, err)
		return
	}
	frise := "État, coupures, signal, latence et débit sur une même frise."
	if n := len(outages); n > 0 {
		frise += " " + plural(n, "coupure", "coupures") + " sur 30 jours."
	}
	v := historyView{common: s.commonFor(r, "Historique"), Entries: []historyEntry{
		{"/chronologie", "chronologie", "Chronologie", frise},
		{"/evenements", "journal", "Événements", journal},
		{"/tendance", "tendance", "Tendance", "Évolution sur 30 jours à un an, avec la température extérieure."},
		{"/rapport", "rapport", "Rapport", "Bilan d'une période, avant et après une intervention, export CSV."},
	}}
	s.render(w, http.StatusOK, "historique", v)
}
