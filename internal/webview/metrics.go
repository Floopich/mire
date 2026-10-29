package webview

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/floopich/mire/internal/metrics"
	"github.com/floopich/mire/internal/settings"
)

// handleMetrics sert /metrics au format Prometheus.
//
// Le point n'existe que si les metriques sont activees. Il ne passe pas par
// la session de l'interface : un collecteur ne se connecte pas. Un jeton,
// s'il est defini, est exige en en-tete Authorization: Bearer.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	st := s.opts.Service.State()
	if !st.Values.Bool(settings.MetricsOn) {
		http.NotFound(w, r)
		return
	}
	if token := st.Values.String(settings.MetricsToken); token != "" {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), []byte(token)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="mire"`)
			http.Error(w, "jeton d'accès requis", http.StatusUnauthorized)
			return
		}
	}
	line, err := s.readStatus(r.Context(), st)
	if err != nil {
		s.opts.Logger.Error("métriques", "erreur", err)
		http.Error(w, "lecture de la base impossible", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", metrics.ContentType)
	w.Header().Set("Cache-Control", "no-store")
	_ = metrics.Write(w, line, metrics.Info{Version: s.opts.Version, Modem: modemName(st), Profile: st.Profile.ID})
}
