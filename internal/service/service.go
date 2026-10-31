// Package service fait tourner les taches de fond de Mire selon les reglages.
// Reload les arrete et les relance avec les nouvelles valeurs.
package service

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/backup"
	"github.com/floopich/mire/internal/collect"
	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/events"
	"github.com/floopich/mire/internal/homeassistant"
	"github.com/floopich/mire/internal/journal"
	"github.com/floopich/mire/internal/modem"
	"github.com/floopich/mire/internal/notify"
	"github.com/floopich/mire/internal/operator"
	"github.com/floopich/mire/internal/probe"
	"github.com/floopich/mire/internal/settings"
	"github.com/floopich/mire/internal/speedtest"
	"github.com/floopich/mire/internal/status"
	"github.com/floopich/mire/internal/store"
	"github.com/floopich/mire/internal/weather"
)

// State decrit la configuration en vigueur.
type State struct {
	Values  settings.Values
	Profile operator.Profile
	// Model est le modem configure ; ModelErr explique pourquoi il manque.
	Model    modem.Model
	ModelErr error
	Address  string
	User     string
	Interval time.Duration
	// Modem est le pilote partage par la collecte et la page Modem : le
	// modem n'accorde qu'une session a la fois. Nil sans configuration.
	Modem modem.Driver
	// Collecting dit si la collecte tourne.
	Collecting bool
	// Probing dit si la sonde de connexion tourne.
	Probing bool
	// Notifying dit si les notifications partent ; NotifyErr explique
	// pourquoi elles sont activees sans pouvoir partir.
	Notifying bool
	NotifyErr error
	// HomeAssistant publie vers Home Assistant quand c'est active ;
	// HomeAssistantErr explique pourquoi la publication ne peut pas partir.
	HomeAssistant    *homeassistant.Publisher
	HomeAssistantErr error
	// Weather releve la meteo quand c'est active ; WeatherErr explique
	// pourquoi le releve ne peut pas commencer.
	Weather    *weather.Fetcher
	WeatherErr error
	// Speed lit les mesures de debit quand c'est active ; SpeedErr explique
	// pourquoi la lecture ne peut pas commencer.
	Speed    *speedtest.Fetcher
	SpeedErr error
}

// Manager porte l'etat et les taches de fond.
type Manager struct {
	// Version est celle du programme, publiee vers Home Assistant.
	Version string
	// Journal recoit le niveau de detail choisi dans les reglages.
	Journal *journal.Journal
	// DataDir est le dossier des donnees, ou vont les sauvegardes
	// automatiques ; vide, il n'y en a pas.
	DataDir string

	db     *sql.DB
	forced settings.Source
	logger *slog.Logger

	mu     sync.RWMutex
	state  State
	cancel context.CancelFunc
	wg     sync.WaitGroup
	// wake previent la collecte qu'il faut recalculer son attente.
	wake chan struct{}
}

// Nudge fait rapprocher les releves sans attendre la fin de l'attente en
// cours : apres une demande de redemarrage, le redemarrage se constate en
// quelques minutes.
func (m *Manager) Nudge() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// New prepare un gestionnaire ; rien ne demarre avant Start.
func New(db *sql.DB, forced settings.Source, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{db: db, forced: forced, logger: logger, wake: make(chan struct{}, 1)}
}

// Resolve calcule la configuration en vigueur sans rien demarrer.
func Resolve(ctx context.Context, db *sql.DB, forced settings.Source) (State, error) {
	values, err := settings.Load(ctx, db, forced)
	if err != nil {
		return State{}, err
	}
	st := State{Values: values, Interval: values.Duration(settings.Interval)}
	if st.Interval < time.Minute {
		st.Interval = 10 * time.Minute
	}

	profile, err := operator.Load(values.String(settings.Profile))
	if err != nil {
		// Un profil supprime ou mal saisi ne doit pas empecher de demarrer :
		// le profil non calibre conserve les releves sans juger.
		profile, _ = operator.Load("generique")
	}
	st.Profile = profile

	id := strings.TrimSpace(values.String(settings.ModemModel))
	if id == "" {
		id = profile.DefaultModem
	}
	switch {
	case id == "":
		st.ModelErr = errors.New("aucun modèle de modem choisi")
	case id == modem.Generic:
		st.ModelErr = modem.ErrUnsupported
	default:
		m, err := modem.Lookup(id)
		if err != nil {
			st.ModelErr = err
		} else {
			st.Model = m
		}
	}

	st.Address = strings.TrimSpace(values.String(settings.ModemURL))
	if st.Address == "" {
		st.Address = profile.DefaultModemURL
	}
	st.User = strings.TrimSpace(values.String(settings.ModemUser))
	if st.User == "" {
		st.User = st.Model.DefaultUser
	}
	return st, nil
}

// ErrNoModemPassword signale un modem pas encore configure.
var ErrNoModemPassword = errors.New("mot de passe du modem non renseigné")

// Driver construit le pilote du modem configure.
func (st State) Driver() (modem.Driver, error) {
	if st.ModelErr != nil {
		return nil, st.ModelErr
	}
	password := st.Values.String(settings.ModemPassword)
	if password == "" {
		return nil, ErrNoModemPassword
	}
	return st.Model.New(modem.Config{URL: st.Address, User: st.User, Password: password})
}

// Rapprochement des releves apres un incident.
const (
	burstInterval = time.Minute
	burstWindow   = 15 * time.Minute
)

// Pace rapproche les releves pendant le quart d'heure qui suit un evenement
// d'alerte du journal : degradation, coupure, redemarrage du modem, saut de
// puissance. Le journal est la seule source : une coupure vue par la sonde
// rapproche aussi les releves du modem.
func (st State) Pace(db *sql.DB, logger *slog.Logger, now func() time.Time) func(context.Context) time.Duration {
	if !st.Values.Bool(settings.Burst) || st.Interval <= burstInterval {
		return nil
	}
	bursting := false
	return func(ctx context.Context) time.Duration {
		last, err := store.LastAlertTime(ctx, db, int(analyze.Warning))
		burst := err == nil && !last.IsZero() && now().Sub(last) < burstWindow
		// Un redemarrage demande depuis Mire rapproche aussi les releves.
		if e, ok, err := store.LastEvent(ctx, db, events.KindRestartAsked); err == nil && ok && now().Sub(e.Time) < burstWindow {
			burst = true
		}
		if burst != bursting {
			bursting = burst
			if burst {
				logger.Info("relevés rapprochés : un incident vient d'être inscrit au journal", "intervalle", burstInterval.String())
			} else {
				logger.Info("fin des relevés rapprochés", "intervalle", st.Interval.String())
			}
		}
		if burst {
			return burstInterval
		}
		return st.Interval
	}
}

// Retention renvoie la politique de conservation.
func (st State) Retention() store.Retention {
	return store.Retention{
		Days:     st.Values.Int(settings.RetentionDays),
		MaxBytes: int64(st.Values.Float(settings.RetentionGB) * float64(1<<30)),
	}
}

// Analyze qualifie un releve avec les reglages en vigueur.
//
// Les erreurs sont comptees sur la fenetre du profil qui precede le releve :
// c'est leur evolution recente, pas le cumul depuis le demarrage du modem,
// qui dit si la ligne perd des donnees.
func (st State) Analyze(ctx context.Context, db *sql.DB, snap docsis.Snapshot) (analyze.Health, error) {
	opts := analyze.Options{OFDMAFloor: st.Values.Int(settings.OFDMAFloor)}
	if st.Profile.Calibrated {
		window := st.Profile.Thresholds.Errors.Window()
		totals, err := store.ErrorTotals(ctx, db, snap.Timestamp.Add(-window), snap.Timestamp)
		if err != nil {
			return analyze.Health{}, err
		}
		opts.Errors = make(map[int]analyze.ErrorCount, len(totals))
		for id, e := range totals {
			opts.Errors[id] = analyze.ErrorCount{Corrected: e.Corrected, Uncorrected: e.Uncorrected}
		}
	}
	return analyze.SnapshotWith(snap, st.Profile, opts), nil
}

// State renvoie la configuration en vigueur.
func (m *Manager) State() State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

// Start lance les taches de fond.
func (m *Manager) Start(ctx context.Context) error {
	return m.Reload(ctx)
}

// Reload relit les reglages et relance les taches de fond.
func (m *Manager) Reload(ctx context.Context) error {
	st, err := Resolve(ctx, m.db, m.forced)
	if err != nil {
		return err
	}
	if m.Journal != nil {
		m.Journal.SetLevel(st.Values.String(settings.LogLevel))
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
		m.wg.Wait()
		m.cancel = nil
	}

	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	m.cancel = cancel

	driver, err := st.Driver()
	switch {
	case errors.Is(err, ErrNoModemPassword):
		m.logger.Info("collecte en attente : modem à configurer")
	case err != nil:
		m.logger.Warn("collecte inactive", "raison", err)
	default:
		st.Collecting = true
		st.Modem = driver
		db := m.db
		info, _ := driver.(modem.InfoReader)
		collector := collect.New(driver, m.db, collect.Options{
			Interval:  st.Interval,
			Retention: st.Retention(),
			Logger:    m.logger,
			Pace:      st.Pace(m.db, m.logger, time.Now),
			Wake:      m.wake,
			Observer: &events.Recorder{
				DB:      db,
				Profile: st.Profile,
				Analyze: func(ctx context.Context, snap docsis.Snapshot) (analyze.Health, error) {
					return st.Analyze(ctx, db, snap)
				},
				Modem:  info,
				Logger: m.logger,
			},
		})
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			m.logger.Info("collecte démarrée", "modele", st.Model.ID, "modem", st.Address,
				"intervalle", st.Interval.String())
			if err := collector.Run(runCtx); err != nil && !errors.Is(err, context.Canceled) {
				m.logger.Error("collecte arrêtée", "erreur", err)
			}
		}()
	}
	if st.Values.Bool(settings.BackupOn) && m.DataDir != "" {
		keeper := &backup.Keeper{DB: m.db, DataDir: m.DataDir, Keep: st.Values.Int(settings.BackupKeep), Logger: m.logger}
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			if err := keeper.Run(runCtx); err != nil && !errors.Is(err, context.Canceled) {
				m.logger.Error("sauvegardes automatiques arrêtées", "erreur", err)
			}
		}()
	}
	if st.Values.Bool(settings.ProbeOn) {
		st.Probing = true
		prober := probe.New(m.db, st.ProbeConfig(m.logger))
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			if err := prober.Run(runCtx); err != nil && !errors.Is(err, context.Canceled) {
				m.logger.Error("sonde arrêtée", "erreur", err)
			}
		}()
	}
	if st.Values.Bool(settings.NotifyOn) {
		sender, err := notify.New(st.Values)
		if err != nil {
			st.NotifyErr = err
			m.logger.Warn("notifications inactives", "raison", err)
		} else {
			st.Notifying = true
			notifier := &notify.Notifier{
				DB: m.db, Sender: sender, Logger: m.logger,
				Filter: st.Values.String(settings.NotifyFilter),
				Link:   st.Values.String(settings.NotifyLink),
			}
			m.wg.Add(1)
			go func() {
				defer m.wg.Done()
				if err := notifier.Run(runCtx); err != nil && !errors.Is(err, context.Canceled) {
					m.logger.Error("notifications arrêtées", "erreur", err)
				}
			}()
		}
	}
	if st.Values.Bool(settings.MQTTOn) {
		cfg := st.HomeAssistantConfig(m.Version)
		if _, ok := st.Modem.(modem.Restarter); ok && st.Values.Bool(settings.MQTTRestart) {
			drv := st.Modem
			cfg.Restart = func(ctx context.Context) {
				if RestartModem(ctx, m.db, drv, "Home Assistant", time.Now, m.logger).Noted() {
					m.Nudge()
				}
			}
		}
		if cfg.Server == "" {
			st.HomeAssistantErr = errors.New("serveur MQTT non renseigné")
			m.logger.Warn("Home Assistant inactif", "raison", st.HomeAssistantErr)
		} else {
			db := m.db
			read := func(ctx context.Context) (status.Status, error) {
				return status.Read(ctx, db, status.Config{Interval: st.Interval,
					Analyze: func(ctx context.Context, snap docsis.Snapshot) (analyze.Health, error) {
						return st.Analyze(ctx, db, snap)
					}})
			}
			pub := homeassistant.New(cfg, read, m.logger)
			st.HomeAssistant = pub
			m.wg.Add(1)
			go func() {
				defer m.wg.Done()
				if err := pub.Run(runCtx); err != nil && !errors.Is(err, context.Canceled) {
					m.logger.Error("Home Assistant arrêté", "erreur", err)
				}
			}()
		}
	}
	if st.Values.Bool(settings.WeatherOn) {
		if f := st.WeatherFetcher(m.db, m.logger); f.Position == "" {
			st.WeatherErr = errors.New("commune ou coordonnées non renseignées")
			m.logger.Warn("météo inactive", "raison", st.WeatherErr)
		} else {
			st.Weather = f
			m.wg.Add(1)
			go func() {
				defer m.wg.Done()
				if err := f.Run(runCtx); err != nil && !errors.Is(err, context.Canceled) {
					m.logger.Error("météo arrêtée", "erreur", err)
				}
			}()
		}
	}
	if st.Values.Bool(settings.SpeedOn) {
		if err := st.SpeedReady(); err != nil {
			st.SpeedErr = err
			m.logger.Warn("débit inactif", "raison", st.SpeedErr)
		} else {
			f := st.SpeedFetcher(m.db, m.logger)
			st.Speed = f
			m.wg.Add(1)
			go func() {
				defer m.wg.Done()
				if err := f.Run(runCtx); err != nil && !errors.Is(err, context.Canceled) {
					m.logger.Error("débit arrêté", "erreur", err)
				}
			}()
		}
	}
	m.state = st
	return nil
}

// SpeedFetcher prepare la lecture des mesures de debit selon les reglages.
func (st State) SpeedFetcher(db *sql.DB, logger *slog.Logger) *speedtest.Fetcher {
	v := st.Values
	var src speedtest.Source = speedtest.NewClient(v.String(settings.SpeedURL), v.String(settings.SpeedToken))
	if v.String(settings.SpeedSource) == settings.SpeedUniFi {
		src = speedtest.NewUniFi(v.String(settings.SpeedURL), v.String(settings.SpeedToken),
			v.String(settings.SpeedUser), v.String(settings.SpeedPassword))
	}
	return &speedtest.Fetcher{DB: db, Logger: logger, Source: src}
}

// SpeedReady dit si la lecture du debit a ce qu'il lui faut, et sinon
// pourquoi.
func (st State) SpeedReady() error {
	v := st.Values
	switch {
	case strings.TrimSpace(v.String(settings.SpeedURL)) == "":
		return errors.New("adresse non renseignée")
	case v.String(settings.SpeedSource) == settings.SpeedUniFi:
		if v.String(settings.SpeedToken) == "" && (v.String(settings.SpeedUser) == "" || v.String(settings.SpeedPassword) == "") {
			return errors.New("clé API ou compte local de la passerelle non renseigné")
		}
	case v.String(settings.SpeedToken) == "":
		return errors.New("jeton de Speedtest Tracker non renseigné")
	}
	return nil
}

// WeatherFetcher prepare le releve de la meteo selon les reglages.
func (st State) WeatherFetcher(db *sql.DB, logger *slog.Logger) *weather.Fetcher {
	return &weather.Fetcher{DB: db, Client: weather.NewClient(), Logger: logger,
		Position: strings.TrimSpace(st.Values.String(settings.WeatherPosition)), Country: st.Profile.Country}
}

// HomeAssistantConfig regle la publication vers Home Assistant.
func (st State) HomeAssistantConfig(version string) homeassistant.Config {
	v := st.Values
	cfg := homeassistant.Config{
		Server: strings.TrimSpace(v.String(settings.MQTTServer)), User: v.String(settings.MQTTUser),
		Password: v.String(settings.MQTTPassword), Prefix: v.String(settings.MQTTPrefix),
		Link: v.String(settings.NotifyLink), Version: version,
	}
	if st.ModelErr == nil {
		cfg.Modem = st.Model.Label()
	}
	return cfg
}

// ProbeConfig regle la sonde de connexion selon les reglages.
func (st State) ProbeConfig(logger *slog.Logger) probe.Config {
	targets := settings.ProbeTargetList(st.Values.String(settings.ProbeTargets))
	if len(targets) == 0 {
		targets = settings.ProbeTargetList(settings.DefaultProbeTargets)
	}
	return probe.Config{
		Targets:  targets,
		Modem:    hostPort(st.Address),
		Interval: st.Values.Duration(settings.ProbeInterval),
		Logger:   logger,
	}
}

// hostPort tire l'hote et le port d'une adresse de modem : la sonde s'y
// connecte en TCP, sans requete HTTP.
func hostPort(address string) string {
	u, err := url.Parse(address)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	return net.JoinHostPort(u.Hostname(), port)
}

// Stop arrete les taches de fond et attend leur fin.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
		m.wg.Wait()
		m.cancel = nil
	}
}

// Forced renvoie les reglages imposes par l'environnement.
func (m *Manager) Forced() settings.Source { return m.forced }
