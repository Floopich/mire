package webview

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/floopich/mire/internal/events"
	"github.com/floopich/mire/internal/modem"
	"github.com/floopich/mire/internal/service"
	"github.com/floopich/mire/internal/settings"
	"github.com/floopich/mire/internal/store"
)

// La page Modem dit ce qu'est le modem et, quand il fait routeur, comment le
// reseau domestique est regle. Elle ne modifie rien, sauf sur demande
// confirmee : le redemarrage.

const (
	// modemTimeout borne la lecture du modem : la page attend, sinon.
	modemTimeout = 45 * time.Second
	// restartGuard est le delai pendant lequel une nouvelle demande de
	// redemarrage est refusee : le temps que le modem revienne.
	restartGuard = service.RestartGuard
)

type modemFact struct {
	Label, Value, Hint string
}

type restartNotice struct {
	Kind    string // classe de .notice : ok, info, alerte
	Message string
	// Restarting dit que le modem redemarre : la page ne l'interroge pas.
	Restarting bool
}

var restartNotices = map[string]restartNotice{
	"demande":      {"ok", "Le modem redémarre. Internet revient d'ici 3 à 5 minutes.", true},
	"sans-reponse": {"info", "Demande envoyée, sans confirmation du modem : il redémarre sans doute.", true},
	"refuse":       {"alerte", "Le modem a refusé la demande. Détail dans Réglages, Journal.", false},
	"erreur":       {"alerte", "Modem injoignable : il n'a pas redémarré.", false},
	"trop-tot":     {"info", "Redémarrage déjà demandé il y a moins de 10 minutes.", false},
}

type modemView struct {
	common
	Address string
	// Problem explique pourquoi le modem ne peut pas etre lu du tout.
	Problem     string
	ProblemLink bool
	Unsupported bool
	// Supported dit que Mire a un pilote pour ce modem.
	Supported bool
	ReadErr   string
	Facts     []modemFact
	Mode      modem.Mode
	// Reseau domestique, quand le modem fait routeur.
	RouterShown bool
	LAN         []modemFact
	Radios      []radioRow
	Hosts       []modem.Host
	HostsActive int
	Missing     string
	RouterErr   string
	CanRestart  bool
	LastAsked   time.Time
	Notice      *restartNotice
	// Reading est la lecture en cours.
	Reading *pending
	// Confirm affiche la demande de confirmation du redemarrage ; TooSoon
	// la remplace quand une demande vient d'etre faite.
	Confirm bool
	TooSoon bool
}

func (s *Server) modemViewFor(r *http.Request, st service.State) modemView {
	v := modemView{common: s.commonFor(r, "Modem"), Address: st.Address}
	v.Supported = st.ModelErr == nil
	switch {
	case errors.Is(st.ModelErr, modem.ErrUnsupported):
		v.Problem = "Modem non pris en charge : seule la sonde Internet fonctionne."
	case st.ModelErr != nil && strings.TrimSpace(st.Values.String(settings.ModemModel)) != "":
		v.Problem, v.ProblemLink = capitalize(st.ModelErr.Error())+".", true
	case st.ModelErr != nil:
		v.Problem, v.ProblemLink = "Aucun modem configuré.", true
	case st.Modem == nil:
		v.Problem, v.ProblemLink = "Mot de passe du modem non renseigné.", true
	default:
		_, v.CanRestart = st.Modem.(modem.Restarter)
	}
	if e, ok, err := store.LastEvent(r.Context(), s.db, events.KindRestartAsked); err == nil && ok {
		v.LastAsked = e.Time
	}
	return v
}

func (s *Server) handleModem(w http.ResponseWriter, r *http.Request) {
	st := s.opts.Service.State()
	v := s.modemViewFor(r, st)
	if n, ok := restartNotices[r.URL.Query().Get("redemarrage")]; ok {
		v.Notice = &n
	}
	if v.Problem != "" || (v.Notice != nil && v.Notice.Restarting) {
		s.render(w, http.StatusOK, "modem", v)
		return
	}

	if _, ok := st.Modem.(modem.InfoReader); !ok {
		v.Unsupported = true
		s.render(w, http.StatusOK, "modem", v)
		return
	}
	res, wait, redirected := s.awaited(w, r, "lecture", modemTimeout, "Lecture du modem",
		func(ctx context.Context, wk *work) any { return s.readModem(ctx, st, wk) })
	switch {
	case redirected:
		return
	case wait != nil:
		v.Reading, v.Refresh = wait, workRefresh
	default:
		v.apply(res.(modemRead), st, s)
	}
	s.render(w, http.StatusOK, "modem", v)
}

// modemRead est ce que la page Modem a lu.
type modemRead struct {
	Info      modem.Info
	InfoErr   error
	Router    *modem.Router
	RouterErr error
}

// readModem lit les informations du modem, puis son reseau domestique
// quand il fait routeur.
func (s *Server) readModem(ctx context.Context, st service.State, wk *work) modemRead {
	var out modemRead
	reader := st.Modem.(modem.InfoReader)
	if out.Info, out.InfoErr = reader.Info(ctx); out.InfoErr != nil {
		s.opts.Logger.Warn("lecture du modem", "modem", st.Address, "erreur", out.InfoErr)
		return out
	}
	if rr, ok := st.Modem.(modem.RouterReader); ok && out.Info.Mode != modem.ModeBridge {
		wk.setStep("Lecture du réseau domestique")
		router, err := rr.Router(ctx)
		if err != nil {
			out.RouterErr = err
			if out.Info.Mode == modem.ModeRouter {
				s.opts.Logger.Warn("lecture du routeur", "modem", st.Address, "erreur", err)
			}
		} else {
			out.Router = &router
		}
	}
	return out
}

// apply remplit la page avec la lecture.
func (v *modemView) apply(rd modemRead, st service.State, s *Server) {
	if rd.InfoErr != nil {
		v.ReadErr = readError(rd.InfoErr, st.Address)
		return
	}
	v.Mode = rd.Info.Mode
	v.Facts = s.factsFor(rd.Info, st)
	switch {
	case rd.Router != nil:
		v.setRouter(*rd.Router)
	case rd.RouterErr != nil && rd.Info.Mode == modem.ModeRouter:
		v.RouterErr = "Réglages réseau non lus. Réessayez dans un instant."
	}
}

func readError(err error, address string) string {
	if errors.Is(err, modem.ErrAuth) {
		return "Le modem refuse l'identifiant ou le mot de passe."
	}
	return "Le modem ne répond pas à " + address + "."
}

// docsisStates traduit l'etat annonce par le modem.
var docsisStates = map[string]string{
	"operational": "En service",
	"online":      "En service",
}

func (s *Server) factsFor(info modem.Info, st service.State) []modemFact {
	var out []modemFact
	add := func(label, value, hint string) {
		if strings.TrimSpace(value) != "" {
			out = append(out, modemFact{label, value, hint})
		}
	}
	name := strings.TrimSpace(info.Manufacturer + " " + info.Model)
	if info.Manufacturer != "" && strings.HasPrefix(strings.ToLower(info.Model), strings.ToLower(info.Manufacturer)) {
		name = info.Model
	}
	if name == "" {
		name = st.Model.Label()
	}
	add("Modèle", name, "")
	add("Logiciel", info.Software, "")
	add("Matériel", info.Hardware, "")
	status := info.Status
	if t, ok := docsisStates[strings.ToLower(status)]; ok {
		status = t
	}
	add("Ligne câble", status, "")
	switch {
	case info.Uptime > 0:
		add("En marche depuis", uptimeText(info.Uptime), "Démarré le "+FmtTime(s.opts.Now().Add(-info.Uptime))+".")
	default:
		add("En marche depuis", info.UptimeText, "")
	}
	switch info.Mode {
	case modem.ModeBridge:
		add("Mode", "Pont (bridge)", "")
	case modem.ModeRouter:
		add("Mode", "Routeur", "")
	}
	return out
}

// handleRestartConfirm demande confirmation : un lien suivi par erreur, ou
// precharge par un navigateur, ne redemarre rien. Elle suit aussi une
// demande en cours.
func (s *Server) handleRestartConfirm(w http.ResponseWriter, r *http.Request) {
	st := s.opts.Service.State()
	v := s.modemViewFor(r, st)
	if wk := s.works.get(r.URL.Query().Get("demande")); wk != nil {
		step, res, done := wk.state()
		if done {
			http.Redirect(w, r, "/modem?redemarrage="+string(res.(service.Outcome)), http.StatusSeeOther)
			return
		}
		v.Title = "Redémarrer le modem"
		v.Confirm, v.Reading, v.Refresh = true, &pending{Step: step, Elapsed: wk.elapsed(time.Now())}, workRefresh
		s.render(w, http.StatusOK, "modem", v)
		return
	}
	if v.Problem != "" || !v.CanRestart {
		http.Redirect(w, r, "/modem", http.StatusSeeOther)
		return
	}
	v.Title = "Redémarrer le modem"
	v.Confirm = true
	v.TooSoon = s.tooSoon(v.LastAsked)
	s.render(w, http.StatusOK, "modem", v)
}

func (s *Server) tooSoon(last time.Time) bool {
	return !last.IsZero() && s.opts.Now().Sub(last) < restartGuard
}

// handleRestart envoie la demande, puis renvoie vers la page Modem : un
// rechargement de la page ne la rejoue pas. Un modem lent a repondre laisse
// place a une page d'attente.
func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "origine refusée", http.StatusForbidden)
		return
	}
	st := s.opts.Service.State()
	v := s.modemViewFor(r, st)
	if _, ok := st.Modem.(modem.Restarter); v.Problem != "" || !ok {
		http.Redirect(w, r, "/modem", http.StatusSeeOther)
		return
	}
	id, wk := s.works.start(modemTimeout, "Envoi de la demande au modem", func(ctx context.Context, _ *work) any {
		out := service.RestartModem(ctx, s.db, st.Modem, "Mire", s.opts.Now, s.opts.Logger)
		// Releves rapproches : le redemarrage se constate en quelques
		// minutes, pas au prochain releve ordinaire.
		if n, ok := s.opts.Service.(interface{ Nudge() }); ok && out.Noted() {
			n.Nudge()
		}
		return out
	})
	if !wk.wait(quickWait) {
		http.Redirect(w, r, "/modem/redemarrer?demande="+id, http.StatusSeeOther)
		return
	}
	_, res, _ := wk.state()
	http.Redirect(w, r, "/modem?redemarrage="+string(res.(service.Outcome)), http.StatusSeeOther)
}

type radioRow struct {
	Band    string
	Enabled bool
	Facts   []modemFact
}

func (v *modemView) setRouter(r modem.Router) {
	v.RouterShown = true
	if r.LANAddress != "" {
		addr := r.LANAddress
		if r.LANMask != "" {
			addr += " (masque " + r.LANMask + ")"
		}
		v.LAN = append(v.LAN, modemFact{Label: "Adresse locale", Value: addr})
	}
	if r.DHCP != nil {
		dhcp := "Inactif : les adresses viennent d'un autre appareil"
		if *r.DHCP {
			dhcp = "Actif : le modem attribue les adresses"
		}
		v.LAN = append(v.LAN, modemFact{Label: "Serveur DHCP", Value: dhcp})
	}
	if len(r.DNS) > 0 {
		v.LAN = append(v.LAN, modemFact{Label: "Serveurs DNS", Value: strings.Join(r.DNS, ", ")})
	}
	for _, radio := range r.Radios {
		row := radioRow{Band: radio.Band, Enabled: radio.Enabled}
		add := func(label, value string) {
			if value != "" {
				row.Facts = append(row.Facts, modemFact{Label: label, Value: value})
			}
		}
		name := radio.SSID
		if name != "" && radio.Hidden {
			name += " (masqué)"
		}
		add("Nom du réseau", name)
		channel := radio.Channel
		if radio.AutoChannel {
			channel = strings.TrimSpace(channel + " (automatique)")
		}
		add("Canal", channel)
		add("Largeur", strings.ReplaceAll(radio.Width, "MHz", nbsp+"MHz"))
		add("Norme", wifiStandard(radio.Standard))
		add("Sécurité", radio.Security)
		v.Radios = append(v.Radios, row)
	}
	v.Hosts = r.Hosts
	for _, h := range r.Hosts {
		if h.Active {
			v.HostsActive++
		}
	}
	v.Missing = strings.Join(r.Missing, ", ")
}

// wifiStandard rend « b,g,n » en « 802.11 b/g/n ».
func wifiStandard(s string) string {
	parts := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return r == ',' || r == '/' || r == ' ' })
	if len(parts) == 0 {
		return ""
	}
	return "802.11" + nbsp + strings.Join(parts, "/")
}

// uptimeText rend une duree de fonctionnement : « 12 jours 3 h ».
func uptimeText(d time.Duration) string {
	days := int(d / (24 * time.Hour))
	if days == 0 {
		return events.Duration(d)
	}
	word := "jours"
	if days == 1 {
		word = "jour"
	}
	out := strconv.Itoa(days) + nbsp + word
	if h := int(d/time.Hour) % 24; h > 0 {
		out += " " + strconv.Itoa(h) + nbsp + "h"
	}
	return out
}
