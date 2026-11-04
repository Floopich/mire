package webview

import (
	"crypto/subtle"
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/service"
	"github.com/floopich/mire/internal/settings"
	"github.com/floopich/mire/internal/status"
)

// bearerOK dit si la requete porte le jeton attendu en Authorization:
// Bearer. Un jeton vide n'exige rien.
func bearerOK(r *http.Request, token string) bool {
	if token == "" {
		return true
	}
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), []byte(token)) == 1
}

// supervisionAllowed dit si l'etat de la ligne peut etre publie pour cette
// requete : option activee, et jeton present s'il est exige.
func supervisionAllowed(r *http.Request, st service.State) bool {
	return st.Values.Bool(settings.SupervisionOn) && bearerOK(r, st.Values.String(settings.SupervisionToken))
}

// lineName nomme l'etat de la ligne pour les outils de supervision.
func lineName(s status.Status) string {
	if !s.Health.Qualified {
		return "non_qualifie"
	}
	return status.LevelName(s.Health.Level)
}

// addSupervision complete la reponse de /sante par l'etat de la ligne et
// d'Internet : des mots et des nombres simples, sans mesure par canal, sans
// adresse ni nom de machine.
func addSupervision(body map[string]any, s status.Status) {
	if s.HasData {
		body["ligne"] = lineName(s)
		body["canaux_a_surveiller"] = s.Summarize().Attention
	}
	if s.HasProbe || s.Outage {
		body["internet"] = "ok"
		if s.Outage {
			body["internet"] = "coupe"
		}
		body["coupures_24h"] = s.Outages24h
		body["micro_coupures_24h"] = s.MicroCuts24h
	}
	if s.HasProbe && s.Probe.RTTAvg != nil {
		body["latence_ms"] = math.Round(*s.Probe.RTTAvg*10) / 10
	}
}

// prtgChannel est un canal du capteur « HTTP Data Advanced » de PRTG.
type prtgChannel struct {
	Channel         string  `json:"channel"`
	Value           float64 `json:"value"`
	Unit            string  `json:"unit,omitempty"`
	CustomUnit      string  `json:"customunit,omitempty"`
	Float           int     `json:"float,omitempty"`
	DecimalMode     string  `json:"decimalmode,omitempty"`
	LimitMode       int     `json:"limitmode,omitempty"`
	LimitMaxWarning *int    `json:"limitmaxwarning,omitempty"`
	LimitMaxError   *int    `json:"limitmaxerror,omitempty"`
	LimitMinError   *int    `json:"limitminerror,omitempty"`
	LimitWarningMsg string  `json:"limitwarningmsg,omitempty"`
	LimitErrorMsg   string  `json:"limiterrormsg,omitempty"`
}

func intp(v int) *int { return &v }

// prtgResult construit la reponse de /sante/prtg. Les seuils d'alerte sont
// fournis : PRTG cree les canaux et leurs limites a la premiere lecture.
func prtgResult(s status.Status, collecting bool, now time.Time) map[string]any {
	var ch []prtgChannel
	fresh := 0.0
	if collecting && s.Fresh {
		fresh = 1
	}
	ch = append(ch, prtgChannel{Channel: "Collecte", Value: fresh, Unit: "Custom", CustomUnit: "à jour",
		LimitMode: 1, LimitMinError: intp(1), LimitErrorMsg: "Plus de relevé récent du modem"})
	text := "Aucun relevé pour l'instant"
	if s.HasData {
		ch = append(ch,
			prtgChannel{Channel: "Ligne", Value: float64(s.Health.Level), Unit: "Custom", CustomUnit: "0-4",
				LimitMode: 1, LimitMaxWarning: intp(int(analyze.Tolerated)), LimitMaxError: intp(int(analyze.Warning)),
				LimitWarningMsg: "Ligne à surveiller", LimitErrorMsg: "Ligne critique"},
			prtgChannel{Channel: "Canaux à surveiller", Value: float64(s.Summarize().Attention), Unit: "Count"},
			prtgChannel{Channel: "Âge du dernier relevé", Value: math.Round(now.Sub(s.Snapshot.Timestamp).Seconds()), Unit: "TimeSeconds"},
		)
		if v := s.Summarize().DSSNRMin; v != nil {
			ch = append(ch, prtgChannel{Channel: "SNR minimum", Value: *v, Unit: "Custom", CustomUnit: "dB", Float: 1, DecimalMode: "1"})
		}
		text = "Ligne : " + strings.ReplaceAll(lineName(s), "_", " ")
		if fresh == 0 {
			text += " · collecte en retard"
		}
	}
	if s.HasProbe || s.Outage {
		up := 1.0
		if s.Outage {
			up = 0
		}
		ch = append(ch,
			prtgChannel{Channel: "Internet", Value: up, Unit: "Custom", CustomUnit: "répond",
				LimitMode: 1, LimitMinError: intp(1), LimitErrorMsg: "Internet coupé"},
			prtgChannel{Channel: "Coupures sur 24 h", Value: float64(s.Outages24h), Unit: "Count"},
			prtgChannel{Channel: "Micro-coupures sur 24 h", Value: float64(s.MicroCuts24h), Unit: "Count"},
		)
		if s.Outage {
			text += " · Internet coupé"
		}
	}
	if s.HasProbe {
		if s.Probe.RTTAvg != nil {
			ch = append(ch, prtgChannel{Channel: "Latence", Value: *s.Probe.RTTAvg, Unit: "TimeResponse", Float: 1, DecimalMode: "1"})
		}
		if loss, ok := s.Loss(); ok {
			ch = append(ch, prtgChannel{Channel: "Perte", Value: loss, Unit: "Percent", Float: 1, DecimalMode: "1"})
		}
	}
	return map[string]any{"prtg": map[string]any{"result": ch, "text": text}}
}

// handlePRTG sert /sante/prtg au format « HTTP Data Advanced » de PRTG.
// Le point n'existe que si l'etat de la ligne est publie.
func (s *Server) handlePRTG(w http.ResponseWriter, r *http.Request) {
	st := s.opts.Service.State()
	if !st.Values.Bool(settings.SupervisionOn) {
		http.NotFound(w, r)
		return
	}
	if !bearerOK(r, st.Values.String(settings.SupervisionToken)) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="mire"`)
		http.Error(w, "jeton d'accès requis", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	line, err := s.readStatus(r.Context(), st)
	if err != nil {
		s.opts.Logger.Error("supervision PRTG", "erreur", err)
		// PRTG lit l'erreur dans le corps : le code reste 200.
		_ = json.NewEncoder(w).Encode(map[string]any{"prtg": map[string]any{"error": 1, "text": "Lecture de la base impossible"}})
		return
	}
	_ = json.NewEncoder(w).Encode(prtgResult(line, st.Collecting, s.opts.Now()))
}

// supervisionView porte les recettes de la rubrique Supervision, remplies
// avec l'adresse par laquelle l'interface est ouverte.
type supervisionView struct {
	Base      string
	Published bool
	Token     bool
	Heartbeat bool
	// Every est l'intervalle conseille aux sondes ; Beat, le delai sans
	// signal de vie au-dela duquel alerter.
	Every, Beat string
}

func requestBase(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = "<machine>:1340"
	}
	return scheme + "://" + host
}

func (s *Server) supervisionView(r *http.Request, st service.State) *supervisionView {
	interval := st.Interval
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	return &supervisionView{
		Base:      requestBase(r),
		Published: st.Values.Bool(settings.SupervisionOn),
		Token:     st.Values.String(settings.SupervisionToken) != "",
		Heartbeat: st.Values.String(settings.HeartbeatURL) != "",
		Every:     every(min(interval, 5*time.Minute)),
		Beat:      every(staleAfter(interval)),
	}
}
