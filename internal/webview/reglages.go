package webview

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/floopich/mire/internal/auth"
	"github.com/floopich/mire/internal/events"
	"github.com/floopich/mire/internal/homeassistant"
	"github.com/floopich/mire/internal/modem"
	"github.com/floopich/mire/internal/notify"
	"github.com/floopich/mire/internal/service"
	"github.com/floopich/mire/internal/settings"
	"github.com/floopich/mire/internal/speedtest"
	"github.com/floopich/mire/internal/store"
)

// fieldView est un champ de la page Reglages.
type fieldView struct {
	settings.Def
	ID      string
	Value   string
	Locked  bool
	Options []settings.Option
	IsSet   bool // mot de passe deja enregistre
	Checked bool
	Type    string // type HTML du champ texte
	// OnlyAttr est la valeur de data-condition. Pas de « data-only » :
	// html/template prend un attribut dont le nom, sans « data- », commence
	// par « on » pour un gestionnaire d'evenement JavaScript, et l'echappe.
	OnlyAttr string
}

// IsBool, IsSelect et IsPassword guident le choix du champ dans le gabarit.
func (f fieldView) IsBool() bool     { return f.Kind == settings.Bool }
func (f fieldView) IsSelect() bool   { return f.Kind == settings.Select }
func (f fieldView) IsPassword() bool { return f.Kind == settings.Password }

type groupView struct {
	ID     string
	Title  string
	Help   string
	Toggle *fieldView
	Fields []fieldView
	Saved  bool
	Err    string
	// Inert marque un groupe desactive par une variable : rien n'y est
	// modifiable, le bouton Enregistrer n'aurait aucun effet.
	Inert bool
}

type testResult struct {
	OK      bool
	Message string
}

// navItem est une rubrique de la liste des reglages : son titre et, en une
// ligne, ce qui est regle aujourd'hui.
type navItem struct {
	ID, Title, Summary, Icon string
	Current                  bool
	// Attention signale une rubrique a completer : sans elle, quelque
	// chose ne fonctionne pas.
	Attention bool
}

// Rubrique hors registre des reglages.
const sectionSecurity = "securite"

type reglagesView struct {
	common
	Nav []navItem
	// Section est la rubrique affichee ; Explicit dit qu'elle a ete choisie.
	// Sur un ecran etroit, la page sans rubrique choisie n'affiche que la
	// liste ; sur un ecran large, liste et rubrique sont cote a cote.
	Section      string
	SectionTitle string
	Explicit     bool
	Group        *groupView
	Detected     []modem.Candidate
	Detecting    bool
	Test         *testResult
	// Testing est l'essai en cours ; Searching, la recherche du modem.
	Testing     *pending
	Searching   *pending
	PasswordOK  bool
	PasswordErr string
	// Log porte les dernieres lignes du service ; NoLog, qu'elles ne sont
	// pas disponibles (demonstration).
	Log   []logLine
	NoLog bool
	// Backups decrit les copies automatiques, pour la rubrique Sauvegarde.
	Backups *backupsView
}

// backupsView resume les copies automatiques.
type backupsView struct {
	Dir   string
	Count int
	Last  time.Time
	Size  string
}

// logLine est une ligne du journal du service, telle que la page l'affiche.
type logLine struct {
	Time, Level, Class, Message, Attrs string
}

// logLevels nomme les niveaux de slog dans les mots de l'interface.
func logLevel(l slog.Level) (label, class string) {
	switch {
	case l >= slog.LevelError:
		return "Erreur", "critique"
	case l >= slog.LevelWarn:
		return "Alerte", "surveiller"
	case l >= slog.LevelInfo:
		return "Info", ""
	default:
		return "Détail", "detail"
	}
}

// logLimit borne le nombre de lignes affichees.
const logLimit = 200

func (s *Server) logLines() []logLine {
	entries := s.opts.Journal.Entries()
	if len(entries) > logLimit {
		entries = entries[:logLimit]
	}
	out := make([]logLine, 0, len(entries))
	for _, e := range entries {
		label, class := logLevel(e.Level)
		out = append(out, logLine{Time: e.Time.Format("02/01 15:04:05"), Level: label, Class: class, Message: e.Message, Attrs: e.Attrs})
	}
	return out
}

// handleLogDownload rend tout le journal garde en memoire, en texte.
func (s *Server) handleLogDownload(w http.ResponseWriter, r *http.Request) {
	if s.opts.Journal == nil {
		http.NotFound(w, r)
		return
	}
	entries := s.opts.Journal.Entries()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="mire-journal-`+s.opts.Now().Format("2006-01-02-1504")+`.txt"`)
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		line := e.Time.Format("2006-01-02 15:04:05") + " " + e.Level.String() + " " + e.Message
		if e.Attrs != "" {
			line += " " + e.Attrs
		}
		_, _ = io.WriteString(w, line+"\n")
	}
}

func fieldID(key string) string { return "r-" + strings.NewReplacer(".", "-", "_", "-").Replace(key) }

// visibleGroups renvoie les groupes qui concernent l'operateur choisi.
func visibleGroups(st service.State) []settings.Group {
	var out []settings.Group
	for _, g := range settings.Groups() {
		if g.Country == "" || g.Country == st.Profile.Country {
			out = append(out, g)
		}
	}
	return out
}

func hasOption(opts []settings.Option, value string) bool {
	for _, o := range opts {
		if o.Value == value {
			return true
		}
	}
	return false
}

// buildGroup prepare un groupe de reglages pour l'affichage ; input reprend
// une saisie refusee.
func buildGroup(g settings.Group, values settings.Values, input map[string]string) groupView {
	gv := groupView{ID: g.ID, Title: g.Title, Help: g.Help}
	for _, d := range g.Defs {
		value := values.String(d.Key)
		if v, ok := input[d.Key]; ok && !values.Locked(d.Key) {
			value = v
		}
		f := fieldView{Def: d, ID: fieldID(d.Key), Value: value, Locked: values.Locked(d.Key), OnlyAttr: d.Only}
		switch d.Kind {
		case settings.Password:
			f.IsSet = value != ""
			f.Value = ""
			f.Type = "password"
		case settings.Bool:
			f.Checked, _ = strconv.ParseBool(value)
		case settings.Select:
			if d.Options != nil {
				f.Options = d.Options()
			}
			// Un modele cache, celui de la demonstration, s'affiche tel quel.
			if d.Key == settings.ModemModel && value != "" && !hasOption(f.Options, value) {
				if m, err := modem.Lookup(value); err == nil {
					f.Options = append(f.Options, settings.Option{Value: m.ID, Label: m.Label()})
				}
			}
		default:
			f.Type = "text"
		}
		if d.Key == g.Toggle {
			ff := f
			gv.Toggle = &ff
			continue
		}
		gv.Fields = append(gv.Fields, f)
	}
	gv.Inert = gv.Toggle != nil && gv.Toggle.Locked && !gv.Toggle.Checked
	return gv
}

var sectionIcons = map[string]string{
	"modem": "modem", "collecte": "collecte", "analyse": "analyse", "sonde": "connexion",
	"notifications": "notifications", "metriques": "metriques", "domotique": "maison", "meteo": "meteo", "abonnement": "abonnement", "debit": "debit", "journal": "note",
	"sauvegarde": "telecharger", sectionSecurity: "verrou",
}

// nav construit la liste des rubriques.
func (s *Server) nav(ctx context.Context, st service.State, current string) []navItem {
	var out []navItem
	for _, g := range visibleGroups(st) {
		summary, attention := s.summary(ctx, g.ID, st)
		out = append(out, navItem{ID: g.ID, Title: g.Title, Summary: summary, Attention: attention,
			Icon: sectionIcons[g.ID], Current: g.ID == current})
	}
	out = append(out,
		navItem{ID: sectionSecurity, Title: "Sécurité", Summary: "Mot de passe de cette interface",
			Icon: sectionIcons[sectionSecurity], Current: current == sectionSecurity})
	return out
}

// summary resume une rubrique en une ligne : ce qui est regle, et si cela
// fonctionne.
func (s *Server) summary(ctx context.Context, id string, st service.State) (string, bool) {
	v := st.Values
	switch id {
	case "modem":
		switch {
		case st.Collecting:
			return st.Model.Name + " · " + strings.TrimPrefix(st.Address, "http://") + " · collecte active", false
		case st.ModelErr != nil:
			return "Modèle à choisir : collecte arrêtée", true
		case v.String(settings.ModemPassword) == "":
			return "Mot de passe à renseigner : collecte arrêtée", true
		default:
			return "Collecte arrêtée", true
		}
	case "collecte":
		out := "Toutes les " + every(st.Interval)
		if v.Bool(settings.Burst) && st.Interval > time.Minute {
			out += ", chaque minute après un incident"
		}
		return out + " · " + retentionSummary(v), false
	case "analyse":
		out := st.Profile.Name
		if st.Profile.Region != "" {
			out += " (" + st.Profile.Region + ")"
		}
		if f := v.Int(settings.OFDMAFloor); f > 0 {
			out += " · OFDMA habituel " + strconv.Itoa(f) + "QAM"
		}
		return out, false
	case "sonde":
		if st.Probing {
			return "Active, toutes les " + every(v.Duration(settings.ProbeInterval)), false
		}
		return "Désactivée", false
	case "notifications":
		switch {
		case st.NotifyErr != nil:
			return "Incomplètes : rien ne part", true
		case st.Notifying:
			return optionLabel(settings.NotifyService, v.String(settings.NotifyService)) + " · " +
				strings.ToLower(optionLabel(settings.NotifyFilter, v.String(settings.NotifyFilter))), false
		default:
			return "Désactivées", false
		}
	case "meteo":
		switch {
		case st.WeatherErr != nil:
			return "Incomplète : rien n'est relevé", true
		case st.Weather == nil:
			return "Désactivée", false
		}
		if p, last, err := st.Weather.Status(); err != nil {
			return "Relevé impossible", true
		} else if !last.IsZero() {
			return "Température de " + p.Name, false
		}
		return "Premier relevé en cours", false
	case "debit":
		switch {
		case st.SpeedErr != nil:
			return "Incomplet : rien n'est lu", true
		case st.Speed == nil:
			return "Désactivé", false
		}
		read, err := st.Speed.Status()
		switch {
		case err != nil:
			return "Lecture impossible", true
		case read.IsZero():
			return "Première lecture en cours", false
		}
		source := optionLabel(settings.SpeedSource, v.String(settings.SpeedSource))
		if last, err := store.LastSpeedTestTime(ctx, s.db); err == nil && s.opts.Now().Sub(last) > speedStale {
			if last.IsZero() {
				return source + " · aucune mesure reçue", true
			}
			return source + " · dernière mesure le " + FmtTime(last), true
		}
		return source + " · " + strings.TrimPrefix(strings.TrimPrefix(v.String(settings.SpeedURL), "http://"), "https://"), false
	case "metriques":
		switch {
		case !v.Bool(settings.MetricsOn):
			return "Désactivées", false
		case v.String(settings.MetricsToken) != "":
			return "Publiées sur /metrics, avec jeton", false
		default:
			return "Publiées sur /metrics, sans jeton", false
		}
	case "domotique":
		switch {
		case st.HomeAssistantErr != nil:
			return "Incomplet : rien ne part", true
		case st.HomeAssistant == nil:
			return "Désactivé", false
		case st.HomeAssistant.Connected():
			return "Publié sur " + mqttHost(v.String(settings.MQTTServer)), false
		case st.HomeAssistant.Err() != nil:
			return "Serveur MQTT injoignable", true
		default:
			return "Connexion à " + mqttHost(v.String(settings.MQTTServer)) + " en cours", false
		}
	case "sauvegarde":
		if !v.Bool(settings.BackupOn) {
			return "Manuelle", false
		}
		return "Chaque jour, " + plural(v.Int(settings.BackupKeep), "copie gardée", "copies gardées"), false
	case "journal":
		return "Niveau " + strings.ToLower(optionLabel(settings.LogLevel, v.String(settings.LogLevel))), false
	case "abonnement":
		if fee := v.Float(settings.MonthlyFee); fee > 0 {
			return euros(fee) + " par mois", false
		}
		return "Prix non renseigné", false
	}
	return "", false
}

// mqttHost rend l'hote d'une adresse de serveur MQTT.
func mqttHost(server string) string {
	if u, err := url.Parse(server); err == nil && u.Host != "" {
		return u.Host
	}
	return server
}

// every rend un intervalle : « 30 s », « 10 min ».
func every(d time.Duration) string {
	if d < time.Minute {
		return strconv.Itoa(int(d.Seconds())) + nbsp + "s"
	}
	return events.Duration(d)
}

func retentionSummary(v settings.Values) string {
	days, gb := v.Int(settings.RetentionDays), v.Float(settings.RetentionGB)
	var age string
	switch {
	case days == 0:
		age = "sans limite de durée"
	case days%365 == 0:
		age = plural(days/365, "an", "ans")
	default:
		age = plural(days, "jour", "jours")
	}
	if gb == 0 {
		return age + ", sans limite de taille"
	}
	return age + ", " + strings.Replace(strconv.FormatFloat(gb, 'f', -1, 64), ".", ",", 1) + nbsp + "Go au plus"
}

// optionLabel renvoie le libelle d'un choix de liste.
func optionLabel(key, value string) string {
	if d, ok := settings.Lookup(key); ok && d.Options != nil {
		for _, o := range d.Options() {
			if o.Value == value {
				return o.Label
			}
		}
	}
	return value
}

// knownSection dit si une rubrique existe pour l'operateur choisi.
func knownSection(st service.State, id string) (settings.Group, bool, bool) {
	if id == sectionSecurity {
		return settings.Group{}, false, true
	}
	for _, g := range visibleGroups(st) {
		if g.ID == id {
			return g, true, true
		}
	}
	return settings.Group{}, false, false
}

// reglagesView prepare la page pour une rubrique ; input reprend une saisie
// refusee.
func (s *Server) reglagesView(r *http.Request, section string, explicit bool, input map[string]string) reglagesView {
	st := s.opts.Service.State()
	v := reglagesView{common: s.commonFor(r, "Réglages"), Section: section, Explicit: explicit}
	v.Nav = s.nav(r.Context(), st, section)
	for _, n := range v.Nav {
		if n.ID == section {
			v.SectionTitle = n.Title
		}
	}
	if explicit {
		v.Title = v.SectionTitle + " – Réglages"
	}
	if g, isGroup, _ := knownSection(st, section); isGroup {
		gv := buildGroup(g, st.Values, input)
		if s.opts.Demo {
			for i := range gv.Fields {
				gv.Fields[i].Env = ""
			}
		}
		gv.Saved = r.URL.Query().Get("enregistre") != ""
		// Un modele jamais choisi s'affiche tel qu'il s'applique : celui que
		// fournit l'operateur, sinon « Autre modem ».
		for i, f := range gv.Fields {
			if f.Def.Key == settings.ModemModel && f.Value == "" {
				gv.Fields[i].Value = effectiveModel(st)
			}
		}
		// Des notifications activees mais incompletes ne partent pas : il
		// faut le dire ici, pas seulement dans le journal du service.
		if g.ID == "notifications" && st.NotifyErr != nil {
			gv.Err = "Les notifications ne partent pas : " + st.NotifyErr.Error() + "."
		}
		if g.ID == "meteo" {
			switch {
			case st.WeatherErr != nil:
				gv.Err = "Rien n'est relevé : " + st.WeatherErr.Error() + "."
			case st.Weather != nil:
				if _, _, err := st.Weather.Status(); err != nil {
					gv.Err = "Le relevé de la météo a échoué : " + err.Error() + ". Mire réessaie dans une demi-heure."
				}
			}
		}
		if g.ID == "debit" {
			switch {
			case st.SpeedErr != nil:
				gv.Err = "Rien n'est lu : " + st.SpeedErr.Error() + "."
			case st.Speed != nil:
				if _, err := st.Speed.Status(); err != nil {
					gv.Err = "La lecture des mesures a échoué : " + err.Error() + ". Mire réessaie dans un quart d'heure."
				}
			}
		}
		if g.ID == "domotique" {
			switch {
			case st.HomeAssistantErr != nil:
				gv.Err = "Rien ne part vers Home Assistant : " + st.HomeAssistantErr.Error() + "."
			case st.HomeAssistant != nil && st.HomeAssistant.Err() != nil:
				gv.Err = "La publication vers Home Assistant est interrompue : " + st.HomeAssistant.Err().Error() +
					". Mire réessaie seul."
			}
		}
		if g.ID == "analyse" {
			s.addObservedOFDMA(r.Context(), &gv)
		}
		if g.ID == "sauvegarde" && !s.opts.Demo {
			v.Backups = s.backups()
		}
		if g.ID == "journal" {
			if s.opts.Journal == nil {
				v.NoLog = true
			} else {
				v.Log = s.logLines()
			}
		}
		v.Group = &gv
	}
	v.PasswordOK = r.URL.Query().Get("mot-de-passe") == "change"
	return v
}

// addObservedOFDMA complete l'aide de la modulation habituelle par ce que
// Mire a releve : le choix s'appuie sur des faits, pas sur une impression.
func (s *Server) addObservedOFDMA(ctx context.Context, gv *groupView) {
	shares, err := store.OFDMAModulations(ctx, s.db, s.opts.Now().AddDate(0, 0, -30))
	if err != nil || len(shares) == 0 {
		return
	}
	total := 0
	for _, m := range shares {
		total += m.Readings
	}
	var parts []string
	for i, m := range shares {
		if i == 3 {
			break
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", m.Modulation, percent(100*float64(m.Readings)/float64(total), 0)))
	}
	for i := range gv.Fields {
		if gv.Fields[i].Key == settings.OFDMAFloor && !gv.Fields[i].Locked {
			gv.Fields[i].Help += " Relevé sur 30 jours : " + strings.Join(parts, ", ") + "."
		}
	}
}

// handleReglages affiche la liste des rubriques, et la premiere a cote sur
// un ecran large.
func (s *Server) handleReglages(w http.ResponseWriter, r *http.Request) {
	s.render(w, http.StatusOK, "reglages", s.reglagesView(r, "modem", false, nil))
}

// handleSection affiche une rubrique.
func (s *Server) handleSection(w http.ResponseWriter, r *http.Request) {
	section := r.PathValue("rubrique")
	if _, _, ok := knownSection(s.opts.Service.State(), section); !ok {
		s.renderError(w, http.StatusNotFound, "Réglages introuvables", "Cette rubrique des réglages n'existe pas.")
		return
	}
	v := s.reglagesView(r, section, true, nil)
	if id := r.URL.Query().Get("essai"); id != "" {
		s.testFor(id, &v)
	}
	if section == "modem" && r.URL.Query().Get("detecter") != "" {
		res, wait, redirected := s.awaited(w, r, "detecter", detectTimeout, "Recherche du modem sur le réseau", s.detect)
		switch {
		case redirected:
			return
		case wait != nil:
			v.Searching, v.Refresh = wait, workRefresh
		default:
			v.Detecting, v.Detected = true, res.([]modem.Candidate)
		}
	}
	s.render(w, http.StatusOK, "reglages", v)
}

// handleSaveGroup enregistre les champs d'un groupe.
func (s *Server) handleSaveGroup(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "origine refusée", http.StatusForbidden)
		return
	}
	g, isGroup, _ := knownSection(s.opts.Service.State(), r.PathValue("rubrique"))
	if !isGroup {
		s.renderError(w, http.StatusNotFound, "Réglages introuvables", "Cette rubrique des réglages n'existe pas.")
		return
	}
	if !s.saveGroup(w, r, g) {
		return
	}
	http.Redirect(w, r, "/reglages/"+g.ID+"?enregistre=1", http.StatusSeeOther)
}

// saveGroup enregistre le formulaire d'un groupe et relance le service.
// Il repond lui-meme en cas d'erreur et renvoie alors false.
func (s *Server) saveGroup(w http.ResponseWriter, r *http.Request, g settings.Group) bool {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "formulaire illisible", http.StatusBadRequest)
		return false
	}
	input := map[string]string{}
	for _, d := range g.Defs {
		if d.Kind == settings.Bool {
			// Une case decochee n'est pas envoyee par le navigateur.
			input[d.Key] = strconv.FormatBool(r.PostForm.Get(d.Key) != "")
			continue
		}
		if vals, present := r.PostForm[d.Key]; present {
			input[d.Key] = vals[0]
		}
	}
	svc := s.opts.Service
	if err := settings.Set(r.Context(), s.db, svc.Forced(), input); err != nil {
		v := s.reglagesView(r, g.ID, true, input)
		v.Group.Err = capitalize(err.Error()) + "."
		v.Group.Saved = false
		s.render(w, http.StatusUnprocessableEntity, "reglages", v)
		return false
	}
	if err := svc.Reload(r.Context()); err != nil {
		s.fail(w, err)
		return false
	}
	return true
}

// Les essais des reglages : le groupe est enregistre, puis l'essai part,
// meme si l'option est encore desactivee : on verifie avant d'activer.
// L'essai tourne en tache de fond ; au-dela d'un instant, la page suit son
// avancement.

// groupTest decrit l'essai d'un groupe.
type groupTest struct {
	group, step string
	run         func(s *Server, ctx context.Context) *testResult
}

var groupTests = map[string]groupTest{
	"modem":         {"modem", "Connexion au modem et lecture des canaux", (*Server).testModem},
	"notifications": {"notifications", "Envoi de la notification d'essai", (*Server).testNotify},
	"meteo":         {"meteo", "Relevé de la température", (*Server).testWeather},
	"debit":         {"debit", "Lecture des mesures de débit", (*Server).testSpeed},
	"domotique":     {"domotique", "Connexion au serveur MQTT", (*Server).testMQTT},
}

// testTimeout borne un essai : la lecture d'un modem lent comprise.
const testTimeout = 2 * time.Minute

// handleTest enregistre le groupe et lance son essai.
func (s *Server) handleTest(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "origine refusée", http.StatusForbidden)
		return
	}
	t, ok := groupTests[r.PathValue("rubrique")]
	if !ok {
		s.renderError(w, http.StatusNotFound, "Réglages introuvables", "Cette rubrique des réglages n'existe pas.")
		return
	}
	g, _ := settings.GroupByID(t.group)
	if !s.saveGroup(w, r, g) {
		return
	}
	id, wk := s.works.start(testTimeout, t.step, func(ctx context.Context, _ *work) any { return t.run(s, ctx) })
	if !wk.wait(quickWait) {
		http.Redirect(w, r, "/reglages/"+t.group+"?essai="+id, http.StatusSeeOther)
		return
	}
	v := s.reglagesView(r, t.group, true, nil)
	_, res, _ := wk.state()
	v.Test = res.(*testResult)
	s.render(w, http.StatusOK, "reglages", v)
}

// detectTimeout borne la recherche du modem sur les adresses usuelles.
const detectTimeout = 10 * time.Second

// detect cherche le modem sur les adresses usuelles.
func (s *Server) detect(ctx context.Context, _ *work) any {
	st := s.opts.Service.State()
	return cableModems(modem.Detect(ctx, modem.Hosts(st.Values.String(settings.ModemURL), st.Profile.DefaultModemURL)))
}

// cableModems ne garde que les modems cable : un routeur ou une autre page
// qui repond n'interesse pas l'utilisateur.
func cableModems(found []modem.Candidate) []modem.Candidate {
	var out []modem.Candidate
	for _, c := range found {
		if c.Model != nil || c.DOCSIS {
			out = append(out, c)
		}
	}
	return out
}

// testFor rend l'essai en cours ou son resultat ; la page se recharge tant
// qu'il tourne.
func (s *Server) testFor(id string, v *reglagesView) {
	wk := s.works.get(id)
	if wk == nil {
		return
	}
	step, res, done := wk.state()
	if done {
		v.Test = res.(*testResult)
		return
	}
	v.Testing = &pending{Step: step, Elapsed: wk.elapsed(time.Now())}
	v.Refresh = workRefresh
}

func (s *Server) testWeather(ctx context.Context) *testResult {
	f := s.opts.Service.State().WeatherFetcher(s.db, s.opts.Logger)
	p, n, err := f.Update(ctx)
	if err != nil {
		return &testResult{Message: "L'essai a échoué : " + err.Error() + "."}
	}
	return &testResult{OK: true, Message: "Température relevée pour " + p.Name + " : " + strconv.Itoa(n) +
		" jours. La page Tendance la rapproche de la ligne."}
}

// speedStale est l'age au-dela duquel la derniere mesure signale une source
// qui ne mesure plus.
const speedStale = 48 * time.Hour

func (s *Server) testSpeed(ctx context.Context) *testResult {
	st := s.opts.Service.State()
	if err := st.SpeedReady(); err != nil {
		return &testResult{Message: "Essai impossible : " + err.Error() + "."}
	}
	name := speedSourceName(st.Values)
	f := st.SpeedFetcher(s.db, s.opts.Logger)
	n, err := f.Update(ctx)
	if err != nil {
		return &testResult{Message: "L'essai a échoué : " + err.Error() + "."}
	}
	last, err := store.LastSpeedTestTime(ctx, s.db)
	if err != nil {
		return &testResult{Message: "L'essai a échoué : " + err.Error() + "."}
	}
	// Une passerelle UniFi ne teste que si ses tests automatiques sont
	// actifs : c'est ce qu'il faut savoir quand rien n'arrive.
	schedule := ""
	if u, ok := f.Source.(*speedtest.UniFi); ok {
		switch on, _, err := u.Schedule(ctx); {
		case err != nil:
			schedule = " Vérifiez ses tests automatiques dans UniFi Network."
		case !on:
			schedule = " Tests automatiques désactivés dans UniFi Network."
		}
	}
	switch {
	case last.IsZero():
		return &testResult{Message: name + " répond, mais n'a encore aucune mesure réussie." + schedule}
	case s.opts.Now().Sub(last) > speedStale:
		return &testResult{Message: name + " répond, mais sa dernière mesure date du " + FmtTime(last) + "." + schedule}
	case n == 0:
		return &testResult{OK: true, Message: name + " répond ; aucune nouvelle mesure depuis celle du " + FmtTime(last) + "." + schedule}
	default:
		return &testResult{OK: true, Message: name + " répond : " + plural(n, "mesure lue", "mesures lues") +
			", la dernière le " + FmtTime(last) + "." + schedule}
	}
}

// speedSourceName nomme la source des mesures de debit.
func speedSourceName(v settings.Values) string {
	if v.String(settings.SpeedSource) == settings.SpeedUniFi {
		return "La passerelle UniFi"
	}
	return "Speedtest Tracker"
}

func (s *Server) testMQTT(ctx context.Context) *testResult {
	st := s.opts.Service.State()
	if err := homeassistant.Test(ctx, st.HomeAssistantConfig(s.opts.Version)); err != nil {
		s.opts.Logger.Warn("essai MQTT", "erreur", err)
		return &testResult{Message: "L'essai a échoué : " + err.Error() + "."}
	}
	return &testResult{OK: true, Message: "Connexion réussie : l'appareil « Mire » apparaît dans Home Assistant."}
}

func (s *Server) testNotify(ctx context.Context) *testResult {
	values := s.opts.Service.State().Values
	sender, err := notify.New(values)
	if err != nil {
		return &testResult{Message: "Essai impossible : " + err.Error() + "."}
	}
	if err := notify.Test(ctx, sender, values.String(settings.NotifyLink)); err != nil {
		s.opts.Logger.Warn("essai de notification", "erreur", err)
		return &testResult{Message: "L'essai n'est pas parti : " + err.Error() + "."}
	}
	return &testResult{OK: true, Message: "Notification d'essai envoyée."}
}

func (s *Server) testModem(ctx context.Context) *testResult {
	st := s.opts.Service.State()
	// Le pilote de la collecte, s'il existe : en ouvrir un second
	// fermerait sa session.
	driver := st.Modem
	if driver == nil {
		d, err := st.Driver()
		if err != nil {
			return &testResult{Message: "Test impossible : " + err.Error() + "."}
		}
		driver = d
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	raw, err := driver.Snapshot(ctx)
	if err != nil {
		// Le detail technique va au journal ; la page dit quoi faire.
		s.opts.Logger.Warn("test du modem", "modem", st.Address, "erreur", err)
		if errors.Is(err, modem.ErrAuth) {
			return &testResult{Message: "Le modem refuse l'identifiant ou le mot de passe."}
		}
		return &testResult{Message: "Le modem ne répond pas à " + st.Address +
			"."}
	}
	return &testResult{OK: true, Message: fmt.Sprintf("Le modem répond : %d canaux descendants et %d montants lus sur %s.",
		len(raw.Downstream), len(raw.Upstream), st.Address)}
}

// handlePassword change le mot de passe de l'interface.
func (s *Server) handlePassword(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "origine refusée", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "formulaire illisible", http.StatusBadRequest)
		return
	}
	fail := func(msg string) {
		v := s.reglagesView(r, sectionSecurity, true, nil)
		v.PasswordErr = msg
		s.render(w, http.StatusUnprocessableEntity, "reglages", v)
	}
	ok, err := auth.Check(r.Context(), s.db, r.PostForm.Get("actuel"))
	if err != nil || !ok {
		fail("Le mot de passe actuel est incorrect.")
		return
	}
	if r.PostForm.Get("nouveau") != r.PostForm.Get("confirmation") {
		fail("Les deux saisies du nouveau mot de passe diffèrent.")
		return
	}
	if err := auth.SetPassword(r.Context(), s.db, r.PostForm.Get("nouveau")); err != nil {
		fail(capitalize(err.Error()) + ".")
		return
	}
	// Toutes les sessions viennent d'etre fermees : on en rouvre une pour
	// l'appareil qui a fait le changement.
	if err := s.openSession(w, r); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/reglages/"+sectionSecurity+"?mot-de-passe=change", http.StatusSeeOther)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	return strings.ToUpper(string(r[0])) + string(r[1:])
}

// reglagesCSS affiche les champs qui ne concernent qu'un choix d'une liste.
//
// La regle depend des valeurs declarees : elle est donc generee plutot
// qu'ecrite a la main dans la feuille de style. Sans :has(), tous les champs
// restent visibles.
func reglagesCSS() string {
	seen := map[string]bool{}
	var rules []string
	for _, g := range settings.Groups() {
		for _, d := range g.Defs {
			if d.Only == "" || seen[d.Only] {
				continue
			}
			seen[d.Only] = true
			key, value, ok := strings.Cut(d.Only, "=")
			if !ok {
				continue
			}
			rules = append(rules, fmt.Sprintf(
				`form:has(select[name=%q] option[value=%q]:checked) .only[data-condition=%q]{display:grid}`,
				key, value, d.Only))
		}
	}
	sort.Strings(rules)
	return "@supports selector(:has(a)){.only{display:none}" + strings.Join(rules, "") + "}\n"
}

// effectiveModel est le modele en vigueur, pour preselectionner une liste.
func effectiveModel(st service.State) string {
	if st.ModelErr == nil {
		return st.Model.ID
	}
	return modem.Generic
}
