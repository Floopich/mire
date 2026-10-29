package modem

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"regexp"
	"strings"
	"time"
)

// Au-dela du releve DOCSIS, un pilote peut offrir la gestion du modem. Chaque
// capacite est une interface distincte, facultative : la page Modem propose
// ce que le pilote sait faire, et dit simplement ce qu'il ne sait pas faire.

// Mode dit qui gere le reseau domestique.
type Mode string

const (
	// ModeUnknown : le modem ne l'a pas dit.
	ModeUnknown Mode = ""
	// ModeBridge : le modem ne fait que modem, un routeur gere le reseau.
	ModeBridge Mode = "pont"
	// ModeRouter : le modem est aussi le routeur et le point d'acces Wi-Fi.
	ModeRouter Mode = "routeur"
)

// Info decrit le modem lui-meme.
type Info struct {
	Model        string
	Manufacturer string
	Hardware     string
	Software     string
	// Uptime est la duree depuis le dernier demarrage ; zero si le format
	// n'a pas ete reconnu, auquel cas UptimeText porte la valeur brute.
	Uptime     time.Duration
	UptimeText string
	Mode       Mode
	// Status est l'etat DOCSIS tel que le modem l'annonce (OPERATIONAL…).
	Status string
}

// Router decrit le reseau domestique quand le modem fait routeur.
type Router struct {
	LANAddress string
	LANMask    string
	// DHCP dit si le modem distribue les adresses ; nil si inconnu.
	DHCP   *bool
	DNS    []string
	Radios []Radio
	Hosts  []Host
	// Missing nomme les parties que le modem n'a pas rendues.
	Missing []string
}

// Radio decrit un reseau Wi-Fi.
type Radio struct {
	Band        string
	Enabled     bool
	SSID        string
	Hidden      bool
	Channel     string
	AutoChannel bool
	Width       string
	Standard    string
	Security    string
}

// Host est un appareil connu du routeur.
type Host struct {
	Name   string
	IP     string
	MAC    string
	Active bool
}

// InfoReader lit les informations du modem.
type InfoReader interface {
	Info(ctx context.Context) (Info, error)
}

// RouterReader lit les reglages du routeur integre, sans rien modifier.
type RouterReader interface {
	Router(ctx context.Context) (Router, error)
}

// Restarter redemarre le modem.
type Restarter interface {
	Restart(ctx context.Context) error
}

var (
	// ErrNoAnswer : la demande est partie mais le modem ne l'a pas
	// confirmee, ce qui arrive quand il redemarre avant d'avoir repondu.
	ErrNoAnswer = errors.New("le modem n'a pas confirmé la demande")
	// ErrRestartRefused : le modem a repondu, mais pas par une acceptation.
	ErrRestartRefused = errors.New("le modem a refusé la demande de redémarrage")
)

// Capture est la reponse brute d'un modem a une lecture, pour le diagnostic.
type Capture struct {
	Path   string          `json:"chemin"`
	Status int             `json:"statut,omitempty"`
	Body   json.RawMessage `json:"reponse,omitempty"`
	// Text porte une reponse qui n'est pas du JSON, tronquee.
	Text  string `json:"texte,omitempty"`
	Error string `json:"erreur,omitempty"`
}

// Capturer releve les reponses brutes du modem, en lecture seule, deja
// anonymisees : c'est ce qu'un contributeur joint a une demande.
type Capturer interface {
	Capture(ctx context.Context, progress Progress) ([]Capture, error)
}

// Progress suit une lecture longue : etape en cours sur le total, et son nom
// en langage courant. Elle peut etre nil.
type Progress func(done, total int, step string)

func (p Progress) report(done, total int, step string) {
	if p != nil {
		p(done, total, step)
	}
}

// masked remplace une valeur qui identifie l'abonne.
const masked = "masqué"

// sensitiveKeys sont des fragments, en minuscules, de noms de champs dont la
// valeur identifie l'abonne ou ouvre l'acces au reseau.
var sensitiveKeys = []string{"pass", "key", "psk", "ssid", "serial", "mac", "physaddress",
	"hostname", "user", "token", "salt", "secret", "wpa", "pin"}

var (
	macPattern  = regexp.MustCompile(`(?i)\b(?:[0-9a-f]{2}[:-]){5}[0-9a-f]{2}\b`)
	ipv4Pattern = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	ipv6Pattern = regexp.MustCompile(`(?i)[0-9a-f]{0,4}(?::[0-9a-f]{0,4}){2,7}`)
)

func sensitive(key string) bool {
	k := strings.ToLower(key)
	for _, s := range sensitiveKeys {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// Anonymize retire d'une reponse ce qui identifie l'abonne : valeurs des
// champs sensibles, adresses MAC, adresses IP publiques. Les adresses
// privees restent : elles disent comment le reseau est organise, sans
// designer personne.
func Anonymize(body []byte) []byte {
	var v any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return []byte(scrub(string(body)))
	}
	out, err := json.MarshalIndent(anonymize(v, false), "", "  ")
	if err != nil {
		return []byte(scrub(string(body)))
	}
	return out
}

func anonymize(v any, hide bool) any {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			// Un numero de version ressemble a une adresse IP (1.2.3.4) :
			// il ne designe personne et reste lisible.
			if s, ok := child.(string); ok && !hide && !sensitive(k) && strings.Contains(strings.ToLower(k), "version") {
				t[k] = s
				continue
			}
			t[k] = anonymize(child, hide || sensitive(k))
		}
		return t
	case []any:
		for i, child := range t {
			t[i] = anonymize(child, hide)
		}
		return t
	case string:
		// Un interrupteur ne designe personne : « SSIDEnable=true » reste lisible.
		if hide && t != "" && !flag(t) {
			return masked
		}
		return scrub(t)
	case json.Number:
		if hide {
			return masked
		}
		return t
	default:
		return t
	}
}

// scrub masque les adresses MAC et IP publiques d'un texte libre.
func scrub(s string) string {
	s = macPattern.ReplaceAllString(s, "xx:xx:xx:xx:xx:xx")
	s = ipv4Pattern.ReplaceAllStringFunc(s, func(m string) string {
		a, err := netip.ParseAddr(m)
		if err != nil || a.As4()[0] >= 224 || !a.IsGlobalUnicast() || a.IsPrivate() {
			return m
		}
		return "x.x.x.x"
	})
	return ipv6Pattern.ReplaceAllStringFunc(s, func(m string) string {
		a, err := netip.ParseAddr(m)
		if err != nil || !a.Is6() || !a.IsGlobalUnicast() || a.IsPrivate() {
			return m
		}
		return "x::x"
	})
}

func flag(s string) bool {
	switch strings.ToLower(s) {
	case "true", "false", "enabled", "disabled", "on", "off":
		return true
	}
	return false
}
