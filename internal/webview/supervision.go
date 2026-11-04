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
	return st.Values.Bool(settings.SupervisionOn) && bearerOK(r, st.Values.String(settings.AccessToken))
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
	if !bearerOK(r, st.Values.String(settings.AccessToken)) {
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

// supervisionView porte les fiches de la rubrique Supervision, une par
// outil, remplies avec l'adresse par laquelle l'interface est ouverte.
type supervisionView struct {
	Tools []toolRecipe
}

// toolRecipe est la fiche d'un outil : les valeurs a recopier dans ses
// reglages, ou une seule phrase quand il manque une publication.
type toolRecipe struct {
	ID, Name string
	Rows     []recipeRow
	Note     string
}

type recipeRow struct {
	Label, Value string
	// Block affiche la valeur sur plusieurs lignes (YAML) ; Plain, comme
	// une indication a suivre plutot qu'une valeur a recopier.
	Block, Plain bool
}

// val est une valeur a recopier, txt une indication.
func val(label, v string) recipeRow { return recipeRow{Label: label, Value: v} }
func txt(label, v string) recipeRow { return recipeRow{Label: label, Value: v, Plain: true} }

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
	return &supervisionView{Tools: recipes(requestBase(r), st.Values, st.Interval)}
}

// recipes construit les fiches. Elles suivent ce qui est publie : une fiche
// ne propose jamais une adresse qui repondrait 404.
func recipes(base string, v settings.Values, interval time.Duration) []toolRecipe {
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	line := v.Bool(settings.SupervisionOn)
	metrics := v.Bool(settings.MetricsOn)
	token := v.String(settings.AccessToken) != ""
	every5 := every(min(interval, 5*time.Minute))
	beat := every(staleAfter(interval))
	const bearer = "Authorization: Bearer <jeton>"
	offLine := "Activez « État de la ligne » ci-dessus."

	kuma := toolRecipe{ID: "kuma", Name: "Uptime Kuma", Rows: []recipeRow{
		txt("Type de sonde", "HTTP(s) - Requête JSON"),
		val("URL", base+"/sante"),
	}}
	if line {
		kuma.Rows = append(kuma.Rows, val("Expression", `$string(collecte = "a_jour" and ligne != "critique")`))
	} else {
		kuma.Rows = append(kuma.Rows, val("Expression", `$string(collecte = "a_jour")`))
	}
	kuma.Rows = append(kuma.Rows, val("Valeur attendue", "true"), txt("Intervalle", every5))
	if line && token {
		kuma.Rows = append(kuma.Rows, val("En-têtes", `{"Authorization": "Bearer <jeton>"}`))
	}
	kuma.Rows = append(kuma.Rows, txt("Signal de vie", "sonde Push, intervalle "+beat))

	prtg := toolRecipe{ID: "prtg", Name: "PRTG", Note: offLine}
	if line {
		prtg = toolRecipe{ID: "prtg", Name: "PRTG", Rows: []recipeRow{
			txt("Capteur", "HTTP Data Advanced"),
			val("URL", base+"/sante/prtg"),
		}}
		if token {
			prtg.Rows = append(prtg.Rows, val("En-tête", bearer))
		}
		prtg.Rows = append(prtg.Rows, txt("Intervalle", every5),
			txt("Canaux et seuils", "fournis par Mire"))
	}

	yaml := "endpoints:\n  - name: Mire\n    url: \"" + base + "/sante\"\n    interval: 5m\n"
	if line && token {
		yaml += "    headers:\n      Authorization: \"Bearer <jeton>\"\n"
	}
	yaml += "    conditions:\n      - \"[STATUS] == 200\"\n      - \"[BODY].collecte == a_jour\""
	if line {
		yaml += "\n      - \"[BODY].ligne != critique\""
	}
	gatus := toolRecipe{ID: "gatus", Name: "Gatus", Rows: []recipeRow{{Label: "config.yaml", Value: yaml, Block: true}}}

	path := "$.collecte"
	if line {
		path = "$.collecte, $.ligne, $.internet"
	}
	zabbix := toolRecipe{ID: "zabbix", Name: "Zabbix, Checkmk", Rows: []recipeRow{
		txt("Élément", "Agent HTTP"),
		val("URL", base+"/sante"),
		val("JSONPath", path),
	}}
	if line && token {
		zabbix.Rows = append(zabbix.Rows, val("En-tête", bearer))
	}

	hc := toolRecipe{ID: "hc", Name: "Healthchecks.io, Better Stack", Rows: []recipeRow{
		txt("Période", beat),
		txt("Son adresse", "dans « Signal de vie » ci-dessus"),
	}}

	prom := toolRecipe{ID: "prom", Name: "Prometheus, Grafana Alloy", Note: "Activez « Métriques Prometheus » ci-dessus."}
	if metrics {
		host := strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
		prom = toolRecipe{ID: "prom", Name: "Prometheus, Grafana Alloy", Rows: []recipeRow{
			val("Cible", host),
			val("Chemin", "/metrics"),
		}}
		if strings.HasPrefix(base, "https://") {
			prom.Rows = append(prom.Rows, val("Schéma", "https"))
		}
		if token {
			prom.Rows = append(prom.Rows, val("Jeton", `authorization: { credentials: "<jeton>" }`))
		}
		prom.Rows = append(prom.Rows, txt("Tableau Grafana", "docs/grafana/mire.json"))
	}
	return []toolRecipe{kuma, prtg, gatus, zabbix, hc, prom}
}
