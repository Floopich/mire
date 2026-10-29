package webview

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/auth"
	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/events"
	"github.com/floopich/mire/internal/journal"
	"github.com/floopich/mire/internal/modem"
	"github.com/floopich/mire/internal/operator"
	"github.com/floopich/mire/internal/service"
	"github.com/floopich/mire/internal/settings"
	"github.com/floopich/mire/internal/status"
	"github.com/floopich/mire/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed assets
var assetFS embed.FS

// Service fournit la configuration en vigueur et relance les taches de fond
// apres un changement de reglage. service.Manager l'implemente.
type Service interface {
	State() service.State
	Reload(ctx context.Context) error
	Forced() settings.Source
}

// Options regle l'interface.
type Options struct {
	DataDir string
	Version string
	// Service porte la configuration en vigueur.
	Service Service
	// Logger recoit les erreurs internes ; slog.Default sinon.
	Logger *slog.Logger
	// Journal garde les dernieres lignes du service, pour les reglages.
	Journal *journal.Journal
	// Now est injectable pour les tests.
	Now func() time.Time
	// Demo ouvre l'interface sans connexion, sur la ligne fictive du mode
	// demonstration ; DemoExit y ajoute le bouton qui la quitte.
	Demo     bool
	DemoExit bool
	// StartDemo prepare une demonstration a cote de l'instance : son
	// interface, et ce qui l'arrete. Nil, l'accueil ne la propose pas.
	StartDemo func(ctx context.Context) (http.Handler, func(), error)
}

// Server sert l'interface.
type Server struct {
	db       *sql.DB
	opts     Options
	pages    map[string]*template.Template
	sessions *auth.Sessions
	limiter  *auth.Limiter
	// css est la feuille generee des reglages ; versions, l'empreinte des
	// feuilles de style appelees avec leur version.
	css      string
	versions map[string]string
	// reports sont les rapports de modem en cours de preparation.
	reports reportJobs
	// works sont les actions lentes en cours : essais, lecture du modem.
	works works
	// demo est la demonstration ouverte depuis l'accueil.
	demo demoState
}

func NewServer(db *sql.DB, opts Options) (*Server, error) {
	if opts.Service == nil {
		return nil, errors.New("webview : service manquant")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Version == "" {
		opts.Version = "dev"
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	s := &Server{
		db: db, opts: opts,
		sessions: auth.NewSessions(db), limiter: auth.NewLimiter(),
		css: reglagesCSS(),
	}
	s.versions = assetVersions(map[string][]byte{"/reglages.css": []byte(s.css)})
	funcs := template.FuncMap{
		"asset":      s.assetURL,
		"fmtValue":   Fmt,
		"fmtMHz":     FmtMHz,
		"fmtTime":    FmtTime,
		"fmtCount":   FmtCount,
		"fmtCount64": func(n int64) string { return FmtCount(int(n)) },
		"label":      Label,
		"shapeD":     shapeD,
		"icon":       icon,
		"levelIcon":  levelIcon,
		"ago":        func(t time.Time) string { return Ago(t, opts.Now()) },
		"hour":       func(t time.Time) string { return t.Local().Format("15:04") },
	}
	pages := map[string]*template.Template{}
	for _, name := range []string{"index", "channel", "login", "error", "reglages", "setup", "evenements", "internet", "comprendre", "tendance", "rapport", "modem", "proposer"} {
		t, err := template.New("layout.html").Funcs(funcs).
			ParseFS(templateFS, "templates/layout.html", "templates/"+name+".html")
		if err != nil {
			return nil, err
		}
		pages[name] = t
	}
	s.pages = pages
	return s, nil
}

// Routes monte les pages et les protections communes.
func (s *Server) Routes() http.Handler {
	static := http.StripPrefix("/static/", http.FileServerFS(staticFS()))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /sante", s.handleHealth)
	mux.HandleFunc("GET /metrics", s.handleMetrics)
	mux.Handle("GET /static/", s.cacheAssets(staticMaxAge, static))
	mux.Handle("GET /manifest.webmanifest", manifestHandler())
	mux.Handle("GET /reglages.css", s.cacheAssets(time.Hour, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = w.Write([]byte(s.css))
	})))
	mux.HandleFunc("POST /demo", s.handleDemoStart)
	mux.HandleFunc("GET /demo", s.handleDemoWait)
	mux.HandleFunc("POST /demo/quitter", s.handleDemoQuit)
	mux.HandleFunc("GET /installation", s.handleSetupStart)
	mux.HandleFunc("POST /installation", s.handleSetupPassword)
	mux.HandleFunc("GET /installation/modem", s.requireAuth(s.handleSetupModem))
	mux.HandleFunc("POST /installation/modem", s.requireAuth(s.handleSetupModemSave))
	mux.HandleFunc("GET /installation/operateur", s.requireAuth(s.handleSetupOperator))
	mux.HandleFunc("GET /installation/options", s.requireAuth(s.handleSetupOptions))
	mux.HandleFunc("POST /installation/operateur", s.requireAuth(s.handleSetupOperatorSave))
	mux.HandleFunc("GET /comprendre", s.requireAuth(s.handleLearn))
	mux.HandleFunc("GET /tendance", s.requireAuth(s.handleTrend))
	mux.HandleFunc("GET /rapport", s.requireAuth(s.handleReport))
	mux.HandleFunc("GET /rapport/donnees/{fichier}", s.requireAuth(s.handleExport))
	mux.HandleFunc("GET /modem", s.requireAuth(s.handleModem))
	mux.HandleFunc("GET /modem/redemarrer", s.requireAuth(s.handleRestartConfirm))
	mux.HandleFunc("POST /modem/redemarrer", s.requireAuth(s.handleRestart))
	mux.HandleFunc("GET /modem/proposer", s.requireAuth(s.handlePropose))
	mux.HandleFunc("POST /modem/proposer", s.requireAuth(s.handleProposeBuild))
	mux.HandleFunc("GET /reglages", s.requireAuth(s.handleReglages))
	mux.HandleFunc("GET /reglages/{rubrique}", s.requireAuth(s.handleSection))
	mux.HandleFunc("POST /reglages/mot-de-passe", s.requireAuth(s.handlePassword))
	mux.HandleFunc("GET /reglages/sauvegarde/telecharger", s.requireAuth(s.handleBackup))
	mux.HandleFunc("GET /reglages/journal/telecharger", s.requireAuth(s.handleLogDownload))
	mux.HandleFunc("POST /reglages/{rubrique}/tester", s.requireAuth(s.handleTest))
	mux.HandleFunc("POST /reglages/{rubrique}", s.requireAuth(s.handleSaveGroup))
	mux.HandleFunc("GET /connexion", s.handleLoginForm)
	mux.HandleFunc("POST /connexion", s.handleLogin)
	mux.HandleFunc("POST /deconnexion", s.handleLogout)
	mux.HandleFunc("GET /{$}", s.requireAuth(s.handleIndex))
	mux.HandleFunc("GET /descendant/{id}", s.requireAuth(s.handleDownstream))
	mux.HandleFunc("GET /montant/{id}", s.requireAuth(s.handleUpstream))
	mux.HandleFunc("GET /evenements", s.requireAuth(s.handleEvents))
	mux.HandleFunc("POST /evenements/notes", s.requireAuth(s.handleAddNote))
	mux.HandleFunc("POST /evenements/notes/{id}/supprimer", s.requireAuth(s.handleDeleteNote))
	mux.HandleFunc("GET /internet", s.requireAuth(s.handleInternet))
	// Ancienne adresse des pages de canal, gardee pour les favoris.
	mux.HandleFunc("GET /canal/{id}", func(w http.ResponseWriter, r *http.Request) {
		target := "/descendant/" + r.PathValue("id")
		if q := r.URL.RawQuery; q != "" {
			target += "?" + q
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.renderError(w, http.StatusNotFound, "Page introuvable",
			"Cette adresse ne correspond à aucune page de Mire.")
	})
	return s.withDemo(securityHeaders(mux))
}

// securityHeaders pose les en-tetes de protection sur chaque reponse : la
// politique de contenu n'autorise que les ressources de l'instance.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; img-src 'self'; font-src 'self'; manifest-src 'self'; "+
			"form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

func cacheFor(d time.Duration, next http.Handler) http.Handler {
	value := "public, max-age=" + strconv.Itoa(int(d.Seconds()))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", value)
		next.ServeHTTP(w, r)
	})
}

// window est une fenetre d'affichage proposee a l'utilisateur.
type window struct {
	Hours  int
	Label  string
	Active bool
}

var windows = []window{{6, "6 h", false}, {24, "24 h", false}, {168, "7 jours", false}, {720, "30 jours", false}}

func windowsFor(hours int) []window {
	out := make([]window, len(windows))
	copy(out, windows)
	for i := range out {
		out[i].Active = out[i].Hours == hours
	}
	return out
}

// common porte ce que toutes les pages connectees affichent.
type common struct {
	Modem   string
	Title   string
	Profile operator.Profile
	Version string
	Hours   int
	Windows []window
	Path    string
	Banner  *banner
	// Refresh recharge la page apres ce nombre de secondes, zero pour jamais.
	Refresh int
	// Demo signale la ligne fictive du mode demonstration ; DemoExit, qu'on
	// peut la quitter pour installer Mire.
	Demo     bool
	DemoExit bool
}

// banner signale un probleme de fonctionnement, distinct de l'etat de la ligne.
type banner struct {
	Kind      string // "info" ou "alerte"
	Message   string
	Link      string
	LinkLabel string
}

type verdict struct {
	Level  analyze.Level
	Title  string
	Detail string
}

type channelRow struct {
	ID         int
	Freq       *float64
	Modulation string
	Power      *float64
	SNR        *float64
	Level      analyze.Level
	Note       string
	LastSeen   time.Time
	Href       string
	// Wide marque un canal OFDM ou OFDMA, dessine differemment dans la bande.
	Wide bool
	// SNRColumn : la voie descendante a un SNR, la voie montante non.
	SNRColumn bool
}

// channelGroup est une famille de canaux d'une voie : SC-QAM (DOCSIS 3.0),
// OFDM ou OFDMA (DOCSIS 3.1). Leurs seuils et leur lecture different : un
// canal OFDM occupe des dizaines de MHz la ou un SC-QAM en occupe 6 ou 8.
type channelGroup struct {
	Label string
	Rows  []channelRow
}

// groupChannels separe les canaux SC-QAM des canaux larges, dans cet ordre.
func groupChannels(rows []channelRow, wide string) []channelGroup {
	var narrow, large []channelRow
	for _, r := range rows {
		if r.Wide {
			large = append(large, r)
		} else {
			narrow = append(narrow, r)
		}
	}
	var out []channelGroup
	if len(narrow) > 0 {
		out = append(out, channelGroup{Label: "SC-QAM", Rows: narrow})
	}
	if len(large) > 0 {
		out = append(out, channelGroup{Label: wide, Rows: large})
	}
	return out
}

type stats struct {
	Readings   int
	First      time.Time
	Latest     time.Time
	DSPowerMin *float64
	DSPowerMax *float64
	DSSNRMin   *float64
	USPowerMax *float64
	DSCount    int
	USCount    int
}

type indexView struct {
	common
	HasData    bool
	Verdict    verdict
	Stats      stats
	Down       []channelRow
	Up         []channelRow
	DownGroups []channelGroup
	UpGroups   []channelGroup
	SpectrumDS *spectrumView
	SpectrumUS *spectrumView
	Next       *progressView
	Legend     []analyze.Level
	// Events sont les derniers evenements, les plus recents d'abord.
	Events []eventRow
}

// recentEvents borne les evenements repris sur la vue d'ensemble.
const recentEvents = 30 * 24 * time.Hour

// modemName rend le modem configure, vide s'il n'y en a pas encore.
func modemName(st service.State) string {
	if st.ModelErr != nil {
		return ""
	}
	return st.Model.Label()
}

func (s *Server) commonFor(r *http.Request, title string) common {
	hours := hoursParam(r)
	st := s.opts.Service.State()
	return common{
		Modem: modemName(st),
		Title: title, Profile: st.Profile, Version: s.opts.Version,
		Hours: hours, Windows: windowsFor(hours), Path: r.URL.Path, Demo: s.opts.Demo, DemoExit: s.opts.DemoExit,
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st := s.opts.Service.State()
	v := indexView{common: s.commonFor(r, "Vue d'ensemble"),
		Legend: []analyze.Level{analyze.Good, analyze.Tolerated, analyze.Warning, analyze.Critical, analyze.Unknown}}

	snap, err := store.LatestSnapshot(ctx, s.db)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		s.fail(w, err)
		return
	default:
		v.HasData = true
	}

	var health analyze.Health
	if v.HasData {
		if health, err = st.Analyze(ctx, s.db, snap); err != nil {
			s.fail(w, err)
			return
		}
		recent, err := store.Events(ctx, s.db, store.EventFilter{
			From: s.opts.Now().Add(-recentEvents), To: s.opts.Now(), Limit: 3})
		if err != nil {
			s.fail(w, err)
			return
		}
		v.Events = toRows(recent, v.Hours)
	}

	since := s.opts.Now().Add(-time.Duration(v.Hours) * time.Hour)
	down, err := store.DownstreamChannels(ctx, s.db, since)
	if err != nil {
		s.fail(w, err)
		return
	}
	up, err := store.UpstreamChannels(ctx, s.db, since)
	if err != nil {
		s.fail(w, err)
		return
	}

	// Frequences et compteurs du dernier releve, par canal : la liste de la
	// fenetre donne les canaux vus, le releve donne leur detail courant.
	dsLatest := map[int]docsis.Downstream{}
	for _, c := range snap.Downstream {
		dsLatest[c.ChannelID] = c
	}
	usLatest := map[int]docsis.Upstream{}
	for _, c := range snap.Upstream {
		usLatest[c.ChannelID] = c
	}
	dsHealth := byChannel(health.Downstream)
	usHealth := byChannel(health.Upstream)

	for _, c := range down {
		row := channelRow{ID: c.ChannelID, Modulation: c.Modulation, Power: c.Power, SNR: c.SNR,
			LastSeen: c.LastSeen, Href: "/descendant/" + strconv.Itoa(c.ChannelID) + "?heures=" + strconv.Itoa(v.Hours),
			Wide: strings.EqualFold(c.Modulation, "OFDM"), SNRColumn: true}
		if d, ok := dsLatest[c.ChannelID]; ok {
			row.Freq = d.FrequencyMHz
		}
		row.Level, row.Note = levelNote(dsHealth, c.ChannelID)
		v.Down = append(v.Down, row)
	}
	for _, c := range up {
		row := channelRow{ID: c.ChannelID, Modulation: c.Modulation, Power: c.Power,
			LastSeen: c.LastSeen, Href: "/montant/" + strconv.Itoa(c.ChannelID) + "?heures=" + strconv.Itoa(v.Hours),
			Wide: strings.EqualFold(c.Modulation, "OFDMA")}
		if u, ok := usLatest[c.ChannelID]; ok {
			row.Freq = u.FrequencyMHz
		}
		row.Level, row.Note = levelNote(usHealth, c.ChannelID)
		v.Up = append(v.Up, row)
	}
	v.DownGroups = groupChannels(v.Down, "OFDM")
	v.UpGroups = groupChannels(v.Up, "OFDMA")
	v.SpectrumDS = Spectrum("Voie descendante", v.Down, 720, 360)
	v.SpectrumUS = Spectrum("Voie montante", v.Up, 260, 0)

	v.Stats = summarize(snap)
	if v.Stats.Readings, err = store.ReadingCount(ctx, s.db); err != nil {
		s.fail(w, err)
		return
	}
	if v.Stats.First, err = store.FirstReadingTime(ctx, s.db); err != nil {
		s.fail(w, err)
		return
	}
	v.Verdict = verdictFor(st.Profile, v.HasData, health)
	if !v.HasData && !st.Collecting {
		v.Verdict.Title, v.Verdict.Detail = "Aucun relevé", "Renseignez le modem pour commencer."
	}
	v.Banner = s.bannerFor(st, v.Stats.Latest)
	// Une coupure en cours passe avant tout le reste.
	if o, ok, err := store.CurrentOutage(ctx, s.db); err != nil {
		s.fail(w, err)
		return
	} else if ok {
		v.Banner = &banner{Kind: "alerte", Link: "/internet", LinkLabel: "Voir le détail",
			Message: o.Detail + " Depuis " + events.Duration(o.Duration(s.opts.Now())) + "."}
	}
	if st.Collecting && v.Banner == nil {
		v.Next = nextReading(v.Stats.Latest, s.opts.Now(), st.Interval)
	}
	v.Refresh = s.refreshAfter(st, v.Stats.Latest)
	s.render(w, http.StatusOK, "index", v)
}

func byChannel(list []analyze.ChannelHealth) map[int]analyze.ChannelHealth {
	out := make(map[int]analyze.ChannelHealth, len(list))
	for _, c := range list {
		out[c.ChannelID] = c
	}
	return out
}

// levelNote retient le niveau d'un canal et la constatation la plus severe.
func levelNote(m map[int]analyze.ChannelHealth, id int) (analyze.Level, string) {
	c, ok := m[id]
	if !ok {
		return analyze.Unknown, ""
	}
	note := ""
	worst := analyze.Unknown
	for _, f := range c.Findings {
		if f.Level > worst && f.Level > analyze.Good {
			worst, note = f.Level, f.Metric+" : "+f.Message
		}
	}
	return c.Level, note
}

func summarize(snap docsis.Snapshot) stats {
	st := stats{Latest: snap.Timestamp, DSCount: len(snap.Downstream), USCount: len(snap.Upstream)}
	for _, c := range snap.Downstream {
		st.DSPowerMin = minPtr(st.DSPowerMin, c.PowerDBmV)
		st.DSPowerMax = maxPtr(st.DSPowerMax, c.PowerDBmV)
		st.DSSNRMin = minPtr(st.DSSNRMin, c.SNRDB)
	}
	for _, c := range snap.Upstream {
		st.USPowerMax = maxPtr(st.USPowerMax, c.PowerDBmV)
	}
	return st
}

func minPtr(cur, v *float64) *float64 {
	if v == nil || (cur != nil && *cur <= *v) {
		return cur
	}
	x := *v
	return &x
}

func maxPtr(cur, v *float64) *float64 {
	if v == nil || (cur != nil && *cur >= *v) {
		return cur
	}
	x := *v
	return &x
}

// verdictFor formule l'etat de la ligne en une phrase.
func verdictFor(profile operator.Profile, hasData bool, h analyze.Health) verdict {
	if !profile.Calibrated {
		return verdict{analyze.Unknown, "Ligne non qualifiée",
			"Relevés conservés, sans seuils pour cet opérateur."}
	}
	if !hasData {
		return verdict{analyze.Unknown, "En attente du premier relevé",
			"Premier relevé dans un instant."}
	}
	flagged := 0
	for _, list := range [][]analyze.ChannelHealth{h.Downstream, h.Upstream} {
		for _, c := range list {
			if c.Level >= analyze.Warning {
				flagged++
			}
		}
	}
	switch h.Level {
	case analyze.Good:
		return verdict{h.Level, "Ligne en bon état", "Toutes les mesures sont dans les plages de l'opérateur."}
	case analyze.Tolerated:
		return verdict{h.Level, "Ligne correcte", "Quelques écarts, dans les tolérances."}
	case analyze.Warning:
		return verdict{h.Level, "Ligne à surveiller", plural(flagged, "canal demande", "canaux demandent") +
			" de l'attention."}
	case analyze.Critical:
		return verdict{h.Level, "Ligne dégradée", plural(flagged, "canal est", "canaux sont") +
			" en état critique."}
	default:
		return verdict{analyze.Unknown, "État inconnu", "Dernier relevé sans mesure exploitable."}
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return FmtCount(n) + " " + many
}

// bannerFor signale une collecte arretee ou muette.
func (s *Server) bannerFor(st service.State, latest time.Time) *banner {
	if !st.Collecting && errors.Is(st.ModelErr, modem.ErrUnsupported) {
		return &banner{Kind: "info", Message: "Modem non pris en charge : seule la sonde Internet fonctionne.",
			Link: "/modem/proposer", LinkLabel: "Proposer mon modem"}
	}
	if !st.Collecting {
		what := "le modem"
		if st.ModelErr == nil && st.Values.String(settings.ModemPassword) == "" {
			what = "le mot de passe du modem"
		}
		msg := "La collecte est arrêtée : renseignez " + what + " dans les réglages."
		if !latest.IsZero() {
			msg += " L'historique reste consultable."
		}
		return &banner{Kind: "info", Message: msg, Link: "/reglages/modem", LinkLabel: "Ouvrir les réglages"}
	}
	if latest.IsZero() {
		return nil
	}
	if late := s.opts.Now().Sub(latest); late > staleAfter(st.Interval) {
		return &banner{Kind: "alerte", Message: "Aucun nouveau relevé depuis " + strings.TrimPrefix(Ago(latest, s.opts.Now()), "il y a ") +
			". Le modem ne répond plus, ou la ligne est coupée."}
	}
	return nil
}

// refreshAfter cale le rechargement de la vue d'ensemble sur l'arrivee du
// releve suivant, avec une marge pour la lecture du modem. Sans collecte, la
// page ne change pas : inutile de la recharger.
func (s *Server) refreshAfter(st service.State, latest time.Time) int {
	if !st.Collecting {
		return 0
	}
	wait := 2 * time.Minute
	if !latest.IsZero() {
		if next := latest.Add(st.Interval).Sub(s.opts.Now()); next > 0 {
			wait = next
		}
	}
	return int((wait + 30*time.Second).Seconds())
}

func staleAfter(interval time.Duration) time.Duration { return status.StaleAfter(interval) }

// readStatus lit l'etat present avec les reglages en vigueur.
func (s *Server) readStatus(ctx context.Context, st service.State) (status.Status, error) {
	return status.Read(ctx, s.db, status.Config{Interval: st.Interval, Now: s.opts.Now,
		Analyze: func(ctx context.Context, snap docsis.Snapshot) (analyze.Health, error) {
			return st.Analyze(ctx, s.db, snap)
		}})
}

type channelView struct {
	common
	Direction string
	Back      string
	Channel   store.ChannelSummary
	Freq      *float64
	PowerBand string
	SNRFloor  string
	Charts    []template.HTML
	Points    int
	// Errors est le graphique des erreurs, voie descendante seulement.
	Errors      template.HTML
	ErrorTotals store.ErrorCount
	Events      []eventRow
}

func (s *Server) handleDownstream(w http.ResponseWriter, r *http.Request) {
	s.handleChannel(w, r, true)
}

func (s *Server) handleUpstream(w http.ResponseWriter, r *http.Request) {
	s.handleChannel(w, r, false)
}

func (s *Server) handleChannel(w http.ResponseWriter, r *http.Request, downstream bool) {
	ctx := r.Context()
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id < 0 {
		s.renderError(w, http.StatusBadRequest, "Numéro de canal invalide",
			"L'adresse doit se terminer par le numéro du canal, par exemple /descendant/12.")
		return
	}
	direction := "descendant"
	summaryFn, historyFn := store.DownstreamChannel, store.DownstreamHistory
	if !downstream {
		direction = "montant"
		summaryFn, historyFn = store.UpstreamChannel, store.UpstreamHistory
	}

	summary, err := summaryFn(ctx, s.db, id)
	if errors.Is(err, sql.ErrNoRows) {
		s.renderError(w, http.StatusNotFound, "Canal inconnu",
			"Aucun relevé ne mentionne le canal "+direction+" "+strconv.Itoa(id)+".")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}

	v := channelView{common: s.commonFor(r, "Canal "+direction+" "+strconv.Itoa(id)),
		Direction: direction, Channel: summary, Back: "/?heures="}
	v.Back += strconv.Itoa(v.Hours)
	to := s.opts.Now()
	from := to.Add(-time.Duration(v.Hours) * time.Hour)
	points, err := historyFn(ctx, s.db, id, from, to)
	if err != nil {
		s.fail(w, err)
		return
	}
	v.Points = len(points)

	profile := s.opts.Service.State().Profile
	t := profile.Thresholds
	var powerBand *[2]float64
	if downstream {
		if b, ok := t.DownstreamPowerFor(summary.Modulation); ok && profile.Calibrated {
			powerBand = &[2]float64{b.Good.Min, b.Good.Max}
		}
	} else if b, ok := t.UpstreamPowerFor(upstreamFamily(summary.Modulation)); ok && profile.Calibrated {
		powerBand = &[2]float64{b.Good.Min, b.Good.Max}
	}
	if powerBand != nil {
		v.PowerBand = dec(powerBand[0]) + " à " + dec(powerBand[1]) + nbsp + "dBmV"
	}
	v.Charts = append(v.Charts, template.HTML(SVG(Series{
		Label: "Puissance du canal " + strconv.Itoa(id), Unit: "dBmV", Band: powerBand,
		Points: Samples(points, func(p store.Point) *float64 { return p.Power }),
	})))

	if downstream {
		var floor *float64
		if b, ok := t.SNRFor(summary.Modulation); ok && profile.Calibrated {
			f := b.GoodMin
			floor = &f
			v.SNRFloor = dec(f) + nbsp + "dB"
		}
		v.Charts = append(v.Charts, template.HTML(SVG(Series{
			Label: "SNR du canal " + strconv.Itoa(id), Unit: "dB", Floor: floor,
			Points: Samples(points, func(p store.Point) *float64 { return p.SNR }),
		})))

		errs, err := store.ErrorHistory(ctx, s.db, id, from, to)
		if err != nil {
			s.fail(w, err)
			return
		}
		for _, e := range errs {
			v.ErrorTotals.Corrected += e.Corrected
			v.ErrorTotals.Uncorrected += e.Uncorrected
		}
		v.Errors = template.HTML(ErrorBars("Erreurs du canal "+strconv.Itoa(id), errs, from, to))
	}

	list, err := store.Events(ctx, s.db, store.EventFilter{From: from, To: to, Direction: direction, ChannelID: id, Limit: 50})
	if err != nil {
		s.fail(w, err)
		return
	}
	v.Events = toRows(list, v.Hours)
	for i := range v.Events {
		v.Events[i].Href = "" // on est deja sur la page du canal
	}
	s.render(w, http.StatusOK, "channel", v)
}

func upstreamFamily(modulation string) string {
	if strings.Contains(strings.ToLower(modulation), "ofdma") {
		return "ofdma"
	}
	return "sc_qam"
}

// hoursParam borne la fenetre demandee.
func hoursParam(r *http.Request) int {
	h, err := strconv.Atoi(r.URL.Query().Get("heures"))
	if err != nil || h <= 0 {
		return 24
	}
	if h > 24*90 {
		return 24 * 90
	}
	return h
}

// handleHealth repond a la supervision, sans authentification et sans rien
// de la ligne.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	latest, err := store.LastReadingTime(r.Context(), s.db)
	st := s.opts.Service.State()
	body := map[string]any{"statut": "ok", "version": s.opts.Version}
	if name := modemName(st); name != "" {
		body["modem"] = name
	}
	status := http.StatusOK
	switch {
	case err != nil:
		body["statut"] = "erreur"
		status = http.StatusServiceUnavailable
	case !st.Collecting:
		body["collecte"] = "inactive"
	case latest.IsZero():
		body["collecte"] = "en_attente"
	case s.opts.Now().Sub(latest) > staleAfter(st.Interval):
		body["collecte"] = "en_retard"
	default:
		body["collecte"] = "a_jour"
	}
	if !latest.IsZero() {
		body["dernier_releve"] = latest.Format(time.RFC3339)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (s *Server) render(w http.ResponseWriter, status int, page string, data any) {
	var buf strings.Builder
	if err := s.pages[page].Execute(&buf, data); err != nil {
		s.opts.Logger.Error("rendu de page", "page", page, "erreur", err)
		http.Error(w, "rendu impossible", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(buf.String()))
}

type errorView struct {
	common
	Level   analyze.Level
	Heading string
	Detail  string
}

func (s *Server) renderError(w http.ResponseWriter, status int, heading, detail string) {
	s.render(w, status, "error", errorView{
		common: s.bareCommon(heading),
		Level:  errorLevel(status), Heading: heading, Detail: detail,
	})
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	s.opts.Logger.Error("erreur interne", "erreur", err)
	s.renderError(w, http.StatusInternalServerError, "Lecture de la base impossible",
		"La base locale n'a pas répondu : "+err.Error()+". Vérifiez l'espace disque et les droits du dossier de données.")
}

// errorLevel donne a une page d'erreur la forme qui lui correspond : une panne
// du service est critique, une adresse inconnue ne l'est pas.
func errorLevel(status int) analyze.Level {
	if status >= 500 {
		return analyze.Critical
	}
	return analyze.Unknown
}

// bareCommon sert les pages hors session (connexion, erreurs,
// installation) : ni operateur ni modem en pied de page.
func (s *Server) bareCommon(title string) common {
	return common{Title: title, Version: s.opts.Version, Demo: s.opts.Demo, DemoExit: s.opts.DemoExit}
}
