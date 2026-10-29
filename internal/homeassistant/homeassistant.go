// Package homeassistant publie l'etat de la ligne vers Home Assistant, par
// MQTT, avec la decouverte automatique : un appareil « Mire » apparait avec
// ses capteurs, sans configuration dans Home Assistant.
//
// Les descriptions des capteurs et leur etat sont retenus par le serveur
// MQTT : Home Assistant les retrouve a son redemarrage. Un message de
// derniere volonte rend les capteurs indisponibles si Mire s'arrete.
//
// Quand le modem sait redemarrer, l'appareil porte aussi un bouton
// « Redemarrer le modem » : Home Assistant publie sur un sujet de commande,
// Mire l'ecoute. Un message retenu n'est jamais pris pour un appui : il
// redemarrerait le modem a chaque reconnexion.
package homeassistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/mqtt"
	"github.com/floopich/mire/internal/status"
)

// Sujets propres a Mire. Un seul Mire par serveur MQTT : c'est le cas
// courant, et des sujets fixes restent lisibles dans un explorateur MQTT.
const (
	base         = "mire"
	availability = base + "/disponibilite"
	stateTopic   = base + "/etat"
	restartTopic = base + "/commande/redemarrer"
	nodeID       = "mire"
	// press est le message publie par un bouton de Home Assistant.
	press = "PRESS"
)

const (
	// interval est le rythme de lecture de l'etat ; une publication ne part
	// que si l'etat change, ou toutes les refreshEvery pour rester vivant.
	interval     = 30 * time.Second
	refreshEvery = 5 * time.Minute
	retryMin     = 30 * time.Second
	retryMax     = 5 * time.Minute
)

// Config regle la publication.
type Config struct {
	Server, User, Password string
	// Prefix est le prefixe de decouverte de Home Assistant.
	Prefix string
	// Link est l'adresse de Mire, lien de l'appareil dans Home Assistant.
	Link    string
	Version string
	Modem   string
	// Restart redemarre le modem ; nil, aucun bouton n'est propose.
	Restart func(context.Context)
}

// Publisher publie l'etat tant que son contexte vit.
type Publisher struct {
	Config Config
	Read   func(context.Context) (status.Status, error)
	Logger *slog.Logger

	mu        sync.Mutex
	last      error
	connected bool
}

// New prepare un publicateur.
func New(cfg Config, read func(context.Context) (status.Status, error), logger *slog.Logger) *Publisher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Publisher{Config: cfg, Read: read, Logger: logger}
}

// Err renvoie la derniere erreur de connexion, nil une fois connecte.
func (p *Publisher) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.last
}

// Connected dit si la connexion au serveur MQTT est etablie.
func (p *Publisher) Connected() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.connected
}

func (p *Publisher) setState(connected bool, err error) {
	p.mu.Lock()
	p.connected, p.last = connected, err
	p.mu.Unlock()
}

// Run publie jusqu'a l'annulation du contexte, en se reconnectant apres une
// coupure.
func (p *Publisher) Run(ctx context.Context) error {
	wait := retryMin
	for {
		start := time.Now()
		err := p.session(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Apres une session qui a tenu, la coupure est neuve : on retente vite.
		if time.Since(start) > retryMax {
			wait = retryMin
		}
		p.setState(false, err)
		p.Logger.Warn("Home Assistant : publication interrompue", "erreur", err, "nouvel_essai", wait.String())
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		wait = min(wait*2, retryMax)
	}
}

// session tient une connexion : decouverte, puis etat a chaque changement.
func (p *Publisher) session(ctx context.Context) error {
	c, err := Connect(ctx, p.Config)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	if p.Config.Restart != nil {
		if err := c.Subscribe(restartTopic); err != nil {
			return err
		}
	}
	p.setState(true, nil)
	p.Logger.Info("Home Assistant : connecté au serveur MQTT", "serveur", p.Config.Server)
	var pressing sync.Mutex

	var last []byte
	var sent time.Time
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		s, err := p.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return p.goodbye(c)
			}
			p.Logger.Error("Home Assistant : lecture de l'état", "erreur", err)
		} else {
			payload := State(s)
			if !bytes.Equal(payload, last) || time.Since(sent) >= refreshEvery {
				if err := c.Publish(mqtt.Message{Topic: stateTopic, Payload: payload, Retain: true}); err != nil {
					return err
				}
				last, sent = payload, time.Now()
			} else if err := c.Ping(); err != nil {
				return err
			}
		}
	waiting:
		for {
			select {
			case <-ctx.Done():
				return p.goodbye(c)
			case <-c.Done():
				return c.Err()
			case m := <-c.Messages():
				p.command(ctx, m, &pressing)
			case <-tick.C:
				break waiting
			}
		}
	}
}

// command traite un message du sujet de commande. Le redemarrage tourne a
// part : la publication de l'etat continue pendant qu'il attend le modem.
func (p *Publisher) command(ctx context.Context, m mqtt.Message, pressing *sync.Mutex) {
	if m.Topic != restartTopic || p.Config.Restart == nil {
		return
	}
	if m.Retain || string(m.Payload) != press {
		p.Logger.Warn("Home Assistant : commande ignorée", "sujet", m.Topic, "retenue", m.Retain)
		return
	}
	if !pressing.TryLock() {
		return // un appui est deja en cours
	}
	go func() {
		defer pressing.Unlock()
		p.Config.Restart(ctx)
	}()
}

// goodbye rend les capteurs indisponibles avant de partir.
func (p *Publisher) goodbye(c *mqtt.Client) error {
	_ = c.Publish(mqtt.Message{Topic: availability, Payload: []byte("offline"), Retain: true})
	return nil
}

// Connect se connecte et publie la decouverte : c'est aussi l'essai de la
// page Reglages.
func Connect(ctx context.Context, cfg Config) (*mqtt.Client, error) {
	if cfg.Server == "" {
		return nil, errors.New("serveur MQTT non renseigné")
	}
	c, err := mqtt.Dial(ctx, mqtt.Options{
		Server: cfg.Server, User: cfg.User, Password: cfg.Password, ClientID: nodeID,
		Will: &mqtt.Message{Topic: availability, Payload: []byte("offline"), Retain: true},
	})
	if err != nil {
		return nil, err
	}
	for _, m := range Discovery(cfg) {
		if err := c.Publish(m); err != nil {
			_ = c.Close()
			return nil, err
		}
	}
	if err := c.Publish(mqtt.Message{Topic: availability, Payload: []byte("online"), Retain: true}); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// Test se connecte, publie la decouverte et se deconnecte.
func Test(ctx context.Context, cfg Config) error {
	c, err := Connect(ctx, cfg)
	if err != nil {
		return err
	}
	return c.Close()
}

// Niveaux de la ligne, tels qu'affiches dans Home Assistant.
var levelLabels = map[analyze.Level]string{
	analyze.Good: "Bon", analyze.Tolerated: "Toléré", analyze.Warning: "À surveiller",
	analyze.Critical: "Critique", analyze.Unknown: "Non qualifié",
}

func levelOptions() []string {
	return []string{"Bon", "Toléré", "À surveiller", "Critique", "Non qualifié"}
}

// state est l'etat publie ; une valeur absente vaut null, que Home Assistant
// affiche comme inconnue.
type state struct {
	// Sondes : un verdict, pret pour une automatisation.
	Line       string  `json:"ligne"`
	Signal     *string `json:"signal"`
	Internet   *string `json:"internet"`
	Modem      string  `json:"modem"`
	Diagnostic string  `json:"diagnostic"`
	// Coupures et evenements.
	LastOutage     *string  `json:"derniere_coupure"`
	OutageDuration *float64 `json:"duree_derniere_coupure"`
	Outages24h     int      `json:"coupures_24h"`
	Outages30d     int      `json:"coupures_30j"`
	LastRestart    *string  `json:"dernier_redemarrage"`
	Uncorrected24h *int64   `json:"erreurs_non_corrigees_24h"`
	// Mesures.
	Attention   *int     `json:"canaux_attention"`
	DSPowerMin  *float64 `json:"puissance_descendante_min"`
	DSPowerMax  *float64 `json:"puissance_descendante_max"`
	DSSNRMin    *float64 `json:"snr_min"`
	USPowerMax  *float64 `json:"emission_max"`
	DSCount     *int     `json:"canaux_descendants"`
	USCount     *int     `json:"canaux_montants"`
	Latency     *float64 `json:"latence"`
	Loss        *float64 `json:"pertes"`
	LastReading *string  `json:"dernier_releve"`
}

// maxState est la longueur maximale d'un etat dans Home Assistant.
const maxState = 255

// State rend l'etat publie.
func State(s status.Status) []byte {
	out := state{Line: levelLabels[analyze.Unknown], Modem: "OFF", Diagnostic: clip(s.Diagnosis(), maxState),
		Outages24h: s.Outages24h, Outages30d: s.Outages30d}
	if s.Fresh {
		out.Modem = "ON"
	}
	if s.HasData {
		sum := s.Summarize()
		if s.Health.Qualified {
			out.Line = levelLabels[s.Health.Level]
			// Un canal tolere n'est pas un probleme : le journal en juge de meme.
			v := "OFF"
			if s.Health.Level >= analyze.Warning {
				v = "ON"
			}
			out.Signal = &v
		}
		out.Attention, out.DSCount, out.USCount = &sum.Attention, &sum.DSCount, &sum.USCount
		out.DSPowerMin, out.DSPowerMax = round(sum.DSPowerMin), round(sum.DSPowerMax)
		out.DSSNRMin, out.USPowerMax = round(sum.DSSNRMin), round(sum.USPowerMax)
		out.LastReading = stamp(s.Snapshot.Timestamp)
		out.Uncorrected24h = &s.Uncorrected24h
	}
	if s.HasProbe || s.Outage {
		v := "ON"
		if s.Outage {
			v = "OFF"
		}
		out.Internet = &v
	}
	if s.HasProbe {
		out.Latency = round(s.Probe.RTTAvg)
		if l, ok := s.Loss(); ok {
			out.Loss = round(&l)
		}
	}
	if s.HasLastOutage {
		out.LastOutage = stamp(s.LastOutage.Start)
		m := math.Round(s.LastOutage.Duration(s.Now).Minutes())
		out.OutageDuration = &m
	}
	if !s.LastRestart.IsZero() {
		out.LastRestart = stamp(s.LastRestart)
	}
	b, _ := json.Marshal(out)
	return b
}

func stamp(t time.Time) *string {
	v := t.UTC().Format(time.RFC3339)
	return &v
}

// clip borne un texte a n caracteres.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// round arrondit au dixieme, la precision des mesures du modem.
func round(v *float64) *float64 {
	if v == nil {
		return nil
	}
	r := math.Round(*v*10) / 10
	return &r
}

// entity decrit un capteur pour la decouverte.
type entity struct {
	component, id, name string
	extra               map[string]any
}

// entities sont les capteurs publies : d'abord les sondes, qui donnent un
// verdict, puis les mesures, puis le diagnostic de l'appareil.
var entities = []entity{
	// Sondes.
	{"binary_sensor", "internet", "Internet", map[string]any{
		"device_class": "connectivity", "value_template": "{{ value_json.internet }}"}},
	{"binary_sensor", "signal", "Signal", map[string]any{
		"device_class": "problem", "icon": "mdi:sine-wave", "value_template": "{{ value_json.signal }}"}},
	{"binary_sensor", "modem", "Modem", map[string]any{
		"device_class": "connectivity", "icon": "mdi:router-network",
		"value_template": "{{ value_json.modem }}"}},
	{"sensor", "ligne", "État de la ligne", map[string]any{
		"device_class": "enum", "options": levelOptions(), "icon": "mdi:list-status",
		"value_template": "{{ value_json.ligne }}"}},
	{"sensor", "diagnostic", "Diagnostic", map[string]any{
		"icon": "mdi:stethoscope", "value_template": "{{ value_json.diagnostic }}"}},

	// Coupures et evenements.
	{"sensor", "derniere_coupure", "Dernière coupure", map[string]any{
		"device_class": "timestamp", "icon": "mdi:lan-disconnect",
		"value_template": "{{ value_json.derniere_coupure }}"}},
	{"sensor", "duree_derniere_coupure", "Durée de la dernière coupure", map[string]any{
		"device_class": "duration", "unit_of_measurement": "min", "icon": "mdi:timer-outline",
		"value_template": "{{ value_json.duree_derniere_coupure }}"}},
	{"sensor", "coupures_24h", "Coupures sur 24 h", map[string]any{
		"state_class": "measurement", "icon": "mdi:counter",
		"value_template": "{{ value_json.coupures_24h }}"}},
	{"sensor", "coupures_30j", "Coupures sur 30 jours", map[string]any{
		"state_class": "measurement", "icon": "mdi:counter",
		"value_template": "{{ value_json.coupures_30j }}"}},
	{"sensor", "dernier_redemarrage", "Dernier redémarrage du modem", map[string]any{
		"device_class": "timestamp", "icon": "mdi:restart",
		"value_template": "{{ value_json.dernier_redemarrage }}"}},
	{"sensor", "erreurs_non_corrigees_24h", "Erreurs non corrigées sur 24 h", map[string]any{
		"state_class": "measurement", "icon": "mdi:alert-octagon-outline",
		"value_template": "{{ value_json.erreurs_non_corrigees_24h }}"}},

	// Mesures.
	{"sensor", "canaux_attention", "Canaux à surveiller", map[string]any{
		"state_class": "measurement", "icon": "mdi:alert-circle-outline",
		"value_template": "{{ value_json.canaux_attention }}"}},
	{"sensor", "puissance_descendante_min", "Puissance descendante minimale", dbmv("puissance_descendante_min", "mdi:download")},
	{"sensor", "puissance_descendante_max", "Puissance descendante maximale", dbmv("puissance_descendante_max", "mdi:download")},
	{"sensor", "snr_min", "SNR le plus bas", map[string]any{
		"unit_of_measurement": "dB", "state_class": "measurement", "icon": "mdi:signal",
		"value_template": "{{ value_json.snr_min }}"}},
	{"sensor", "emission_max", "Émission la plus forte", dbmv("emission_max", "mdi:upload")},
	{"sensor", "latence", "Latence", map[string]any{
		"device_class": "duration", "unit_of_measurement": "ms", "state_class": "measurement",
		"value_template": "{{ value_json.latence }}"}},
	{"sensor", "pertes", "Pertes", map[string]any{
		"unit_of_measurement": "%", "state_class": "measurement", "icon": "mdi:lan-disconnect",
		"value_template": "{{ value_json.pertes }}"}},

	// Diagnostic de l'appareil.
	{"sensor", "canaux_descendants", "Canaux descendants", map[string]any{
		"state_class": "measurement", "icon": "mdi:arrow-down-bold", "entity_category": "diagnostic",
		"value_template": "{{ value_json.canaux_descendants }}"}},
	{"sensor", "canaux_montants", "Canaux montants", map[string]any{
		"state_class": "measurement", "icon": "mdi:arrow-up-bold", "entity_category": "diagnostic",
		"value_template": "{{ value_json.canaux_montants }}"}},
	{"sensor", "dernier_releve", "Dernier relevé", map[string]any{
		"device_class": "timestamp", "entity_category": "diagnostic",
		"value_template": "{{ value_json.dernier_releve }}"}},
}

func dbmv(key, icon string) map[string]any {
	return map[string]any{"unit_of_measurement": "dBmV", "state_class": "measurement", "icon": icon,
		"value_template": "{{ value_json." + key + " }}"}
}

// Discovery rend les messages de decouverte, retenus.
func Discovery(cfg Config) []mqtt.Message {
	prefix := cfg.Prefix
	if prefix == "" {
		prefix = "homeassistant"
	}
	device := map[string]any{"identifiers": []string{nodeID}, "name": "Mire", "manufacturer": "Mire",
		"sw_version": cfg.Version}
	if cfg.Modem != "" {
		device["model"] = cfg.Modem
	}
	if cfg.Link != "" {
		device["configuration_url"] = cfg.Link
	}
	var out []mqtt.Message
	button := prefix + "/button/" + nodeID + "/redemarrer/config"
	if cfg.Restart != nil {
		b, _ := json.Marshal(map[string]any{
			"name": "Redémarrer le modem", "unique_id": nodeID + "_redemarrer",
			"default_entity_id": "button." + nodeID + "_redemarrer",
			"command_topic":     restartTopic, "payload_press": press, "retain": false,
			"device_class": "restart", "entity_category": "config",
			"availability_topic": availability, "device": device,
		})
		out = append(out, mqtt.Message{Topic: button, Payload: b, Retain: true})
	} else {
		// Une configuration vide retire le bouton d'une publication passee.
		out = append(out, mqtt.Message{Topic: button, Retain: true})
	}
	for _, e := range entities {
		c := map[string]any{
			// default_entity_id remplace object_id, retire par Home Assistant :
			// l'identifiant complet, domaine compris, fixe a la premiere decouverte.
			"name": e.name, "unique_id": nodeID + "_" + e.id, "default_entity_id": e.component + "." + nodeID + "_" + e.id,
			"state_topic": stateTopic, "availability_topic": availability, "device": device,
		}
		for k, v := range e.extra {
			c[k] = v
		}
		b, _ := json.Marshal(c)
		out = append(out, mqtt.Message{Topic: prefix + "/" + e.component + "/" + nodeID + "/" + e.id + "/config",
			Payload: b, Retain: true})
	}
	return out
}
