package settings

import (
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/floopich/mire/internal/modem"
	"github.com/floopich/mire/internal/operator"
)

// Cles des reglages, utilisees par le reste du programme.
const (
	ModemURL      = "modem.adresse"
	ModemModel    = "modem.modele"
	ModemUser     = "modem.identifiant"
	ModemPassword = "modem.mot_de_passe"
	Interval      = "collecte.intervalle"
	Burst         = "collecte.rapprochee"
	Profile       = "operateur.profil"
	OFDMAFloor    = "analyse.ofdma_plancher"
	MonthlyFee    = "abonnement.redevance"
	RetentionDays = "conservation.jours"
	RetentionGB   = "conservation.taille_go"
	ProbeOn       = "sonde.active"
	ProbeTargets  = "sonde.cibles"
	ProbeInterval = "sonde.intervalle"

	NotifyOn      = "notif.active"
	NotifyService = "notif.service"
	NotifyFilter  = "notif.filtre"
	NotifyLink    = "notif.adresse"
	NtfyURL       = "notif.ntfy.serveur"
	NtfyTopic     = "notif.ntfy.sujet"
	NtfyToken     = "notif.ntfy.jeton"
	GotifyURL     = "notif.gotify.serveur"
	GotifyToken   = "notif.gotify.jeton"
	DiscordURL    = "notif.discord.webhook"
	TelegramToken = "notif.telegram.jeton"
	TelegramChat  = "notif.telegram.chat"
	SlackURL      = "notif.slack.webhook"
	MailServer    = "notif.mail.serveur"
	MailSecurity  = "notif.mail.securite"
	MailUser      = "notif.mail.identifiant"
	MailPassword  = "notif.mail.mot_de_passe"
	MailFrom      = "notif.mail.expediteur"
	MailTo        = "notif.mail.destinataire"
	WebhookURL    = "notif.webhook.url"

	WeatherOn       = "meteo.active"
	WeatherPosition = "meteo.position"

	SpeedOn       = "debit.actif"
	SpeedSource   = "debit.source"
	SpeedURL      = "debit.adresse"
	SpeedToken    = "debit.jeton"
	SpeedUser     = "debit.identifiant"
	SpeedPassword = "debit.mot_de_passe"

	MetricsOn    = "metriques.actives"
	MetricsToken = "metriques.jeton"

	MQTTOn       = "mqtt.actif"
	MQTTServer   = "mqtt.serveur"
	MQTTUser     = "mqtt.identifiant"
	MQTTPassword = "mqtt.mot_de_passe"
	MQTTPrefix   = "mqtt.prefixe"
	MQTTRestart  = "mqtt.redemarrage"
)

// Sources des mesures de debit.
const (
	SpeedTracker = "speedtest-tracker"
	SpeedUniFi   = "unifi"
)

// DefaultMQTTPrefix est le prefixe de decouverte de Home Assistant.
const DefaultMQTTPrefix = "homeassistant"

// DefaultProbeTargets sont trois resolveurs DNS publics, joignables en TCP
// sur le port 443 depuis presque tous les reseaux, et repartis entre trois
// operateurs : une panne de l'un d'eux n'est pas une coupure de la ligne.
const DefaultProbeTargets = "1.1.1.1:443 8.8.8.8:443 9.9.9.9:443"

// Groups renvoie les groupes de reglages, dans l'ordre de la page.
func Groups() []Group {
	return []Group{
		{
			ID:    "modem",
			Title: "Modem",
			Help:  "Sans mot de passe, la collecte reste arrêtée.",
			Defs: []Def{
				{Key: ModemModel, Env: "MIRE_MODELE", Kind: Select, Label: "Modèle",
					Help: "Absent de la liste : « Autre modem ».", Options: modelOptions},
				{Key: ModemURL, Env: "MIRE_MODEM", Kind: Text, Label: "Adresse",
					Help:        "Vide : celle du profil.",
					Placeholder: "http://192.168.100.1", Check: checkURL, Normalize: withScheme("http://")},
				{Key: ModemUser, Env: "MIRE_UTILISATEUR", Kind: Text, Label: "Identifiant",
					Help: "Vide : celui du modèle."},
				{Key: ModemPassword, Env: "MIRE_MODEM_MOT_DE_PASSE", Kind: Password, Label: "Mot de passe",
					Help: "Vide : garde l'actuel."},
			},
		},
		{
			ID:    "collecte",
			Title: "Collecte et conservation",
			Help:  "La première limite atteinte l'emporte.",
			Defs: []Def{
				{Key: Interval, Env: "MIRE_INTERVALLE", Kind: Duration, Default: "10m", Label: "Intervalle entre deux relevés",
					Help: "Une minute au minimum.", Check: checkInterval},
				{Key: Burst, Env: "MIRE_RAPPROCHE", Kind: Bool, Default: "true", Label: "Relevés rapprochés après un incident",
					Help: "Une minute entre deux relevés pendant le quart d'heure qui suit un incident."},
				{Key: RetentionDays, Env: "MIRE_RETENTION", Kind: Integer, Default: "3650", Label: "Durée de conservation (jours)",
					Help: "0 pour illimitée.", Check: checkPositive},
				{Key: RetentionGB, Env: "MIRE_TAILLE_MAX", Kind: Number, Default: "5", Label: "Taille maximale de la base (Go)",
					Help: "0 pour illimitée.", Check: checkPositive},
			},
		},
		{
			ID:    "analyse",
			Title: "Opérateur et seuils",

			Defs: []Def{
				{Key: Profile, Env: "MIRE_PROFIL", Kind: Select, Default: "voo", Label: "Profil d'opérateur",
					Help: "Abonné Orange : votre réseau câble, VOO ou Telenet.", Options: profileOptions},
				{Key: OFDMAFloor, Env: "MIRE_OFDMA_PLANCHER", Kind: Select, Label: "Modulation habituelle de la voie montante OFDMA",
					Help: "Si votre segment reste en basse modulation.",
					Options: func() []Option {
						out := []Option{{"", "Celle du profil d'opérateur"}}
						for _, q := range []string{"1024", "512", "256", "128", "64", "32", "16", "8"} {
							out = append(out, Option{q, q + "QAM"})
						}
						return out
					}},
			},
		},
		{
			ID:      "abonnement",
			Title:   "Abonnement",
			Help:    "Pour estimer la compensation légale des coupures de plus de 8 heures.",
			Country: "BE",
			Defs: []Def{
				{Key: MonthlyFee, Env: "MIRE_ABONNEMENT", Kind: Number, Label: "Prix mensuel de l'abonnement (€)",
					Help:        "Facultatif. Pour un pack, son prix total.",
					Placeholder: "60", Check: checkPositive},
			},
		},
		{
			ID:     "sonde",
			Title:  "Internet",
			Help:   "Vérifie qu'Internet répond, mesure la latence et date les coupures.",
			Toggle: ProbeOn,
			Defs: []Def{
				{Key: ProbeOn, Env: "MIRE_SONDE", Kind: Bool, Default: "true", Label: "Surveiller la connexion à Internet"},
				{Key: ProbeTargets, Env: "MIRE_SONDE_CIBLES", Kind: Text, Default: DefaultProbeTargets, Label: "Cibles",
					Help:        "hôte:port, séparés par des espaces.",
					Placeholder: DefaultProbeTargets, Check: checkTargets},
				{Key: ProbeInterval, Env: "MIRE_SONDE_INTERVALLE", Kind: Duration, Default: "30s", Label: "Intervalle",
					Help: "De 10 secondes à 10 minutes.", Check: checkProbeInterval},
			},
		},
		{
			ID:     "debit",
			Title:  "Débit",
			Help:   "Mesures de Speedtest Tracker ou d'une passerelle UniFi.",
			Toggle: SpeedOn,
			Defs: []Def{
				{Key: SpeedOn, Env: "MIRE_DEBIT", Kind: Bool, Default: "false", Label: "Lire les mesures de débit"},
				{Key: SpeedSource, Env: "MIRE_DEBIT_SOURCE", Kind: Select, Default: SpeedTracker, Label: "Source",
					Help: "UniFi : planifiez les tests de débit dans UniFi Network.",
					Options: func() []Option {
						return []Option{{SpeedTracker, "Speedtest Tracker"}, {SpeedUniFi, "Passerelle UniFi"}}
					}},
				{Key: SpeedURL, Env: "MIRE_DEBIT_ADRESSE", Kind: Text, Label: "Adresse",
					Help:  "Speedtest Tracker : http://192.168.1.10:8080. UniFi : https://192.168.1.1, sans port.",
					Check: checkURL, Normalize: withScheme("http://")},
				{Key: SpeedToken, Env: "MIRE_DEBIT_JETON", Kind: Password, Label: "Jeton ou clé API",
					Help: "Speedtest Tracker : droit « Read results ». UniFi : clé de UniFi Network, ou compte local."},
				{Key: SpeedUser, Env: "MIRE_DEBIT_IDENTIFIANT", Kind: Text, Label: "Compte local UniFi",
					Help: "Compte local, sans double authentification.",
					Only: SpeedSource + "=" + SpeedUniFi},
				{Key: SpeedPassword, Env: "MIRE_DEBIT_MOT_DE_PASSE", Kind: Password, Label: "Mot de passe du compte local",
					Only: SpeedSource + "=" + SpeedUniFi},
			},
		},
		{
			ID:     "notifications",
			Title:  "Notifications",
			Help:   "Ligne dégradée, coupure, modem injoignable, et leur fin.",
			Toggle: NotifyOn,
			Defs: []Def{
				{Key: NotifyOn, Env: "MIRE_NOTIF", Kind: Bool, Default: "false", Label: "Envoyer des notifications"},
				{Key: NotifyService, Env: "MIRE_NOTIF_SERVICE", Kind: Select, Default: "ntfy", Label: "Service",
					Options: func() []Option {
						return []Option{{"ntfy", "ntfy"}, {"gotify", "Gotify"}, {"discord", "Discord"},
							{"telegram", "Telegram"}, {"slack", "Slack"}, {"mail", "Mail"}, {"webhook", "Webhook"}}
					}},
				{Key: NotifyFilter, Env: "MIRE_NOTIF_FILTRE", Kind: Select, Default: "etat", Label: "Événements envoyés",

					Options: func() []Option {
						return []Option{{"etat", "État de la ligne et coupures"}, {"critiques", "Alertes critiques seulement"},
							{"tout", "Tout le journal"}}
					}},
				{Key: NotifyLink, Env: "MIRE_NOTIF_ADRESSE", Kind: Text, Label: "Adresse de Mire",
					Help:        "Facultatif : lien vers le journal dans chaque message.",
					Placeholder: "https://mire.exemple.be", Check: checkURL, Normalize: withScheme("https://")},

				{Key: NtfyURL, Env: "MIRE_NTFY_SERVEUR", Kind: Text, Default: "https://ntfy.sh", Label: "Serveur ntfy",
					Only: NotifyService + "=ntfy", Check: checkURL},
				{Key: NtfyTopic, Env: "MIRE_NTFY_SUJET", Kind: Text, Label: "Sujet",
					Help: "Sur ntfy.sh, choisissez-le difficile à deviner.",
					Only: NotifyService + "=ntfy"},
				{Key: NtfyToken, Env: "MIRE_NTFY_JETON", Kind: Password, Label: "Jeton d'accès",
					Help: "Facultatif.", Only: NotifyService + "=ntfy"},

				{Key: GotifyURL, Env: "MIRE_GOTIFY_SERVEUR", Kind: Text, Label: "Serveur Gotify",
					Placeholder: "https://gotify.exemple.be", Only: NotifyService + "=gotify", Check: checkURL},
				{Key: GotifyToken, Env: "MIRE_GOTIFY_JETON", Kind: Password, Label: "Jeton de l'application",
					Help: "Gotify, onglet Apps.", Only: NotifyService + "=gotify"},

				{Key: DiscordURL, Env: "MIRE_DISCORD_WEBHOOK", Kind: Password, Label: "Adresse du webhook",
					Help: "Salon, Intégrations, Webhooks.", Only: NotifyService + "=discord", Check: checkURL},

				{Key: TelegramToken, Env: "MIRE_TELEGRAM_JETON", Kind: Password, Label: "Jeton du bot",
					Help: "Donné par @BotFather.", Only: NotifyService + "=telegram"},
				{Key: TelegramChat, Env: "MIRE_TELEGRAM_CHAT", Kind: Text, Label: "Identifiant de la conversation",
					Only: NotifyService + "=telegram"},

				{Key: SlackURL, Env: "MIRE_SLACK_WEBHOOK", Kind: Password, Label: "Adresse du webhook",
					Only: NotifyService + "=slack", Check: checkURL},

				{Key: MailServer, Env: "MIRE_MAIL_SERVEUR", Kind: Text, Label: "Serveur d'envoi",
					Placeholder: "smtp.exemple.be:587", Only: NotifyService + "=mail", Check: checkHostPort},
				{Key: MailSecurity, Env: "MIRE_MAIL_SECURITE", Kind: Select, Default: "starttls", Label: "Chiffrement",
					Only: NotifyService + "=mail",
					Options: func() []Option {
						return []Option{{"starttls", "STARTTLS (port 587)"}, {"tls", "TLS (port 465)"}, {"aucun", "Aucun (réseau local seulement)"}}
					}},
				{Key: MailUser, Env: "MIRE_MAIL_IDENTIFIANT", Kind: Text, Label: "Identifiant",
					Help: "Vide si le serveur n'en demande pas.", Only: NotifyService + "=mail"},
				{Key: MailPassword, Env: "MIRE_MAIL_MOT_DE_PASSE", Kind: Password, Label: "Mot de passe",
					Only: NotifyService + "=mail"},
				{Key: MailFrom, Env: "MIRE_MAIL_EXPEDITEUR", Kind: Text, Label: "Adresse mail de l'expéditeur",
					Placeholder: "mire@exemple.be", Only: NotifyService + "=mail", Check: checkMail},
				{Key: MailTo, Env: "MIRE_MAIL_DESTINATAIRE", Kind: Text, Label: "Adresse mail du destinataire",
					Help: "Séparées par des virgules.", Only: NotifyService + "=mail", Check: checkMail},

				{Key: WebhookURL, Env: "MIRE_WEBHOOK_URL", Kind: Text, Label: "Adresse du webhook",
					Help: "Chaque événement en JSON, par POST.", Only: NotifyService + "=webhook", Check: checkURL},
			},
		},
		{
			ID:     "meteo",
			Title:  "Météo",
			Help:   "Compare la ligne à la température extérieure (Open-Meteo, sans compte).",
			Toggle: WeatherOn,
			Defs: []Def{
				{Key: WeatherOn, Env: "MIRE_METEO", Kind: Bool, Default: "false", Label: "Relever la température"},
				{Key: WeatherPosition, Env: "MIRE_METEO_POSITION", Kind: Text, Label: "Commune ou coordonnées",
					Help:        "Commune, ou latitude, longitude.",
					Placeholder: "Namur, ou 50.47, 4.87"},
			},
		},
		{
			ID:     "metriques",
			Title:  "Métriques",
			Help:   "Format Prometheus, sur /metrics.",
			Toggle: MetricsOn,
			Defs: []Def{
				{Key: MetricsOn, Env: "MIRE_METRIQUES", Kind: Bool, Default: "false", Label: "Publier les métriques"},
				{Key: MetricsToken, Env: "MIRE_METRIQUES_JETON", Kind: Password, Label: "Jeton d'accès",
					Help: "Facultatif : Authorization: Bearer."},
			},
		},
		{
			ID:     "domotique",
			Title:  "Home Assistant",
			Help:   "Appareil « Mire » découvert par Home Assistant via MQTT.",
			Toggle: MQTTOn,
			Defs: []Def{
				{Key: MQTTOn, Env: "MIRE_MQTT", Kind: Bool, Default: "false", Label: "Publier vers Home Assistant"},
				{Key: MQTTServer, Env: "MIRE_MQTT_SERVEUR", Kind: Text, Label: "Serveur MQTT",
					Help:        "mqtt:// ou mqtts://.",
					Placeholder: "mqtt://192.168.1.10:1883", Check: checkMQTT},
				{Key: MQTTUser, Env: "MIRE_MQTT_IDENTIFIANT", Kind: Text, Label: "Identifiant",
					Help: "Vide si le serveur n'en demande pas."},
				{Key: MQTTPassword, Env: "MIRE_MQTT_MOT_DE_PASSE", Kind: Password, Label: "Mot de passe"},
				{Key: MQTTPrefix, Env: "MIRE_MQTT_PREFIXE", Kind: Text, Default: DefaultMQTTPrefix, Label: "Préfixe de découverte",
					Help: "homeassistant par défaut.", Check: checkTopic},
				{Key: MQTTRestart, Env: "MIRE_MQTT_REDEMARRAGE", Kind: Bool, Default: "true", Label: "Bouton « Redémarrer le modem »",
					Help: "Si le modèle le permet ; une demande par tranche de 10 minutes."},
			},
		},
	}
}

func modelOptions() []Option {
	var out []Option
	for _, m := range modem.Models() {
		out = append(out, Option{Value: m.ID, Label: m.Label()})
	}
	return append(out, Option{Value: modem.Generic, Label: "Autre modem, non pris en charge"})
}

func profileOptions() []Option {
	profiles, err := operator.All()
	if err != nil {
		return nil
	}
	// Les profils calibres d'abord : « non calibre » est le dernier recours.
	sort.SliceStable(profiles, func(i, j int) bool { return profiles[i].Calibrated && !profiles[j].Calibrated })
	var out []Option
	for _, p := range profiles {
		label := p.Name
		if p.Region != "" {
			label += " (" + p.Region + ")"
		}
		out = append(out, Option{Value: p.ID, Label: label})
	}
	return out
}

// withScheme ajoute le schema a une adresse saisie sans lui : on ecrit
// volontiers « mire.exemple.be » ou « 192.168.100.1 ».
func withScheme(scheme string) func(string) string {
	return func(v string) string {
		v = strings.TrimSpace(v)
		if v == "" || strings.Contains(v, "://") {
			return v
		}
		return scheme + strings.TrimLeft(v, "/")
	}
}

func checkURL(v string) error {
	if v == "" {
		return nil
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("adresse attendue sous la forme http://192.168.100.1")
	}
	return nil
}

func checkMQTT(v string) error {
	if v == "" {
		return nil
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "mqtt" && u.Scheme != "mqtts") || u.Hostname() == "" {
		return errors.New("adresse attendue sous la forme mqtt://192.168.1.10:1883")
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return errors.New("port invalide")
		}
	}
	return nil
}

// checkTopic refuse les jokers et les separateurs d'un sujet MQTT.
func checkTopic(v string) error {
	if strings.ContainsAny(v, "#+ ") || strings.HasPrefix(v, "/") || strings.HasSuffix(v, "/") {
		return errors.New("un mot sans espace, sans + ni #, par exemple homeassistant")
	}
	return nil
}

func checkInterval(v string) error {
	if v == "" {
		return nil
	}
	d, err := time.ParseDuration(v)
	if err == nil && d < time.Minute {
		// Le firmware met une dizaine de secondes a assembler ses tables :
		// en dessous d'une minute, les releves se chevaucheraient.
		return errors.New("une minute au minimum")
	}
	return nil
}

func checkPositive(v string) error {
	if strings.HasPrefix(strings.TrimSpace(v), "-") {
		return errors.New("valeur négative")
	}
	return nil
}

// ProbeTargetList decoupe la liste des cibles de la sonde.
func ProbeTargetList(v string) []string {
	return strings.FieldsFunc(v, func(r rune) bool { return r == ' ' || r == ',' || r == ';' || r == '\n' || r == '\t' })
}

func checkTargets(v string) error {
	list := ProbeTargetList(v)
	if len(list) == 0 {
		return nil // vide : les cibles par defaut
	}
	if len(list) > 5 {
		return errors.New("cinq cibles au maximum")
	}
	for _, t := range list {
		host, port, err := net.SplitHostPort(t)
		if err != nil || host == "" {
			return fmt.Errorf("%q : forme attendue hôte:port, par exemple 1.1.1.1:443", t)
		}
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("%q : port invalide", t)
		}
	}
	return nil
}

func checkProbeInterval(v string) error {
	if v == "" {
		return nil
	}
	d, err := time.ParseDuration(v)
	if err == nil && (d < 10*time.Second || d > 10*time.Minute) {
		return errors.New("entre 10 secondes et 10 minutes")
	}
	return nil
}

func checkHostPort(v string) error {
	if v == "" {
		return nil
	}
	host, port, err := net.SplitHostPort(v)
	if err != nil || host == "" {
		return errors.New("forme attendue serveur:port, par exemple smtp.exemple.be:587")
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return errors.New("port invalide")
	}
	return nil
}

func checkMail(v string) error {
	for _, a := range strings.Split(v, ",") {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if _, err := mail.ParseAddress(a); err != nil {
			return fmt.Errorf("%q : adresse mail invalide", a)
		}
	}
	return nil
}
