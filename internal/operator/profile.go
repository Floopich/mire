// Package operator porte les profils d'operateur.
//
// Tout ce qui varie d'un operateur a l'autre est une donnee, pas du code :
// l'adresse par defaut du modem, le modem fourni, et les seuils qui
// qualifient l'etat de la ligne. Ajouter un operateur revient a ecrire un
// fichier JSON dans profiles/, pas a toucher au programme.
package operator

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

//go:embed profiles/*.json
var profileFS embed.FS

// Range est un intervalle ferme, en dBmV.
type Range struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// Contains indique si une valeur tombe dans l'intervalle.
func (r Range) Contains(v float64) bool { return v >= r.Min && v <= r.Max }

// PowerBands qualifie une puissance par intervalles emboites.
type PowerBands struct {
	Good     Range `json:"good"`
	Warning  Range `json:"warning"`
	Critical Range `json:"critical"`
}

// SNRBands qualifie un rapport signal/bruit par planchers.
//
// Contrairement a la puissance, seul le bas compte : un SNR trop eleve n'est
// pas un defaut.
type SNRBands struct {
	GoodMin     float64 `json:"good_min"`
	WarningMin  float64 `json:"warning_min"`
	CriticalMin float64 `json:"critical_min"`
}

// ModulationLimits borne la modulation acceptable en voie montante.
type ModulationLimits struct {
	CriticalMaxQAM  int `json:"critical_max_qam"`
	WarningMaxQAM   int `json:"warning_max_qam"`
	ToleratedMaxQAM int `json:"tolerated_max_qam,omitempty"`
	// Usual est la modulation habituelle declaree par l'abonne ; elle ne
	// vient jamais d'un profil.
	Usual int `json:"-"`
}

// ErrorLimits borne les erreurs de la voie descendante.
//
// Les compteurs du modem cumulent depuis son demarrage : seules leurs
// variations d'un releve a l'autre disent ce qui se passe maintenant. La
// proportion d'erreurs non corrigees se juge donc sur une fenetre glissante,
// et seulement au-dela d'un volume minimal, sous lequel un pourcentage ne
// signifie rien.
type ErrorLimits struct {
	UncorrectablePctWarning  float64 `json:"uncorrectable_pct_warning"`
	UncorrectablePctCritical float64 `json:"uncorrectable_pct_critical"`
	// WindowHours est la fenetre de jugement ; 24 h par defaut, pour couvrir
	// les heures de pointe du soir.
	WindowHours int `json:"window_hours"`
	// MinCodewords est le nombre d'erreurs, corrigees ou non, en dessous
	// duquel la proportion n'est pas jugee ; 1 000 par defaut.
	MinCodewords int64 `json:"min_codewords"`
	// SpikeUncorrectable est le nombre d'erreurs non corrigees, sur toute la
	// ligne et entre deux releves, qui fait inscrire un pic au journal ;
	// 1 000 par defaut.
	SpikeUncorrectable int64 `json:"spike_uncorrectable"`
}

// Window renvoie la fenetre de jugement des erreurs.
func (e ErrorLimits) Window() time.Duration {
	if e.WindowHours <= 0 {
		return 24 * time.Hour
	}
	return time.Duration(e.WindowHours) * time.Hour
}

// MinVolume renvoie le volume minimal pour juger une proportion.
func (e ErrorLimits) MinVolume() int64 {
	if e.MinCodewords <= 0 {
		return 1000
	}
	return e.MinCodewords
}

// Spike renvoie le seuil d'un pic d'erreurs non corrigees.
func (e ErrorLimits) Spike() int64 {
	if e.SpikeUncorrectable <= 0 {
		return 1000
	}
	return e.SpikeUncorrectable
}

// Thresholds est le jeu de seuils complet d'un profil.
//
// Les cartes sont indexees par modulation ("256QAM", "ofdm", "sc_qam"...).
// DefaultKey donne la modulation a utiliser quand le modem en annonce une
// qu'on ne connait pas : mieux vaut un seuil approchant qu'aucune analyse.
type Thresholds struct {
	DownstreamPower    map[string]PowerBands `json:"downstream_power"`
	DownstreamDefault  string                `json:"downstream_default"`
	UpstreamPower      map[string]PowerBands `json:"upstream_power"`
	UpstreamDefault    string                `json:"upstream_default"`
	SNR                map[string]SNRBands   `json:"snr"`
	SNRDefault         string                `json:"snr_default"`
	UpstreamModulation ModulationLimits      `json:"upstream_modulation"`
	UpstreamOFDMA      ModulationLimits      `json:"upstream_ofdma"`
	Errors             ErrorLimits           `json:"errors"`
}

// Profile decrit un operateur et la facon d'analyser ses lignes.
type Profile struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Region string `json:"region"`
	// Country est le code ISO du pays de l'operateur : il decide des regles
	// legales qui s'appliquent, comme la compensation belge.
	Country string `json:"country,omitempty"`
	// TimeZone est le fuseau des heures affichees quand TZ n'est pas defini.
	TimeZone        string `json:"time_zone,omitempty"`
	DefaultModemURL string `json:"default_modem_url"`
	// DefaultModem est l'identifiant du pilote a utiliser quand l'abonne n'en
	// choisit pas : le modem fourni par l'operateur.
	DefaultModem string     `json:"default_modem,omitempty"`
	Calibrated   bool       `json:"calibrated"`
	Source       string     `json:"source,omitempty"`
	Notes        string     `json:"notes,omitempty"`
	Thresholds   Thresholds `json:"thresholds"`
}

// DownstreamPowerFor renvoie les bandes de puissance descendante applicables.
func (t Thresholds) DownstreamPowerFor(modulation string) (PowerBands, bool) {
	return lookup(t.DownstreamPower, modulation, t.DownstreamDefault)
}

// UpstreamPowerFor renvoie les bandes de puissance montante applicables.
func (t Thresholds) UpstreamPowerFor(modulation string) (PowerBands, bool) {
	return lookup(t.UpstreamPower, modulation, t.UpstreamDefault)
}

// SNRFor renvoie les planchers de SNR applicables.
func (t Thresholds) SNRFor(modulation string) (SNRBands, bool) {
	return lookup(t.SNR, modulation, t.SNRDefault)
}

func lookup[T any](table map[string]T, key, fallback string) (T, bool) {
	var zero T
	if len(table) == 0 {
		return zero, false
	}
	if v, ok := table[normalize(key)]; ok {
		return v, true
	}
	if v, ok := table[fallback]; ok {
		return v, true
	}
	return zero, false
}

// normalize ramene les variantes d'ecriture du modem a une cle unique.
//
// Le CGA4233 ecrit "256QAM", d'autres firmwares "256 QAM" ou "qam256". On
// tolere les trois plutot que de perdre l'analyse sur un espace.
func normalize(modulation string) string {
	s := strings.ToLower(strings.TrimSpace(modulation))
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "-", "")
	if strings.HasPrefix(s, "qam") && len(s) > 3 {
		s = s[3:] + "qam"
	}
	if strings.HasSuffix(s, "qam") {
		return strings.ToUpper(strings.TrimSuffix(s, "qam")) + "QAM"
	}
	return s
}

// Load renvoie le profil demande.
func Load(id string) (Profile, error) {
	data, err := profileFS.ReadFile("profiles/" + id + ".json")
	if err != nil {
		return Profile{}, fmt.Errorf("profil %q inconnu", id)
	}
	var p Profile
	if err := json.Unmarshal(data, &p); err != nil {
		return Profile{}, fmt.Errorf("profil %q illisible: %w", id, err)
	}
	if p.ID != id {
		return Profile{}, fmt.Errorf("le profil %q déclare l'identifiant %q", id, p.ID)
	}
	return p, nil
}

// All renvoie tous les profils embarques, tries par identifiant.
func All() ([]Profile, error) {
	entries, err := profileFS.ReadDir("profiles")
	if err != nil {
		return nil, err
	}
	var out []Profile
	for _, e := range entries {
		id := strings.TrimSuffix(e.Name(), ".json")
		p, err := Load(id)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
