package webview

import (
	"encoding/csv"
	"net/http"
	"strings"
	"time"

	"github.com/floopich/mire/internal/store"
)

// handleExport sert un tableau brut de la periode, en CSV : horodatage ISO
// 8601, virgule entre les colonnes, point decimal. Il s'ouvre dans un tableur
// ou un outil d'analyse.
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	name, ok := strings.CutSuffix(r.PathValue("fichier"), ".csv")
	e, known := store.ExportByName(name)
	if !ok || !known {
		http.NotFound(w, r)
		return
	}
	from, to, msg := reportPeriod(r.URL.Query(), midnight(s.opts.Now()))
	if msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}
	file := "mire-" + e.Name + "-" + from.Format(time.DateOnly) + "-" + to.AddDate(0, 0, -1).Format(time.DateOnly) + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+file+`"`)
	w.Header().Set("Cache-Control", "no-store")
	cw := csv.NewWriter(w)
	err := e.Write(r.Context(), s.db, from, to, func(row []string) error { return cw.Write(row) })
	cw.Flush()
	if err == nil {
		err = cw.Error()
	}
	if err != nil {
		// L'en-tete est parti : le fichier sera tronque, le journal dit pourquoi.
		s.opts.Logger.Error("export CSV", "tableau", e.Name, "erreur", err)
	}
}
