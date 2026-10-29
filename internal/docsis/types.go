// Package docsis porte le modele de donnees d'un releve DOCSIS.
//
// Les pilotes produisent un RawSnapshot fidele a ce que renvoie le modem :
// valeurs en chaines, unites collees aux nombres, champs parfois absents.
// La normalisation vers Snapshot est faite ici, en un seul endroit, pour que
// chaque pilote n'ait a se soucier que de son propre format d'origine.
package docsis

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// FlexInt accepte un entier ecrit en nombre ou en chaine.
//
// Le CGA4233 renvoie parfois "channelID": 1 et parfois "channelID": "1" selon
// la version de firmware. Un decodage strict rejetterait le releve entier pour
// un guillemet ; on prefere lire les deux.
//
// Il tient sur 64 bits sur toutes les architectures : les compteurs d'erreurs
// du modem depassent 2^31 (3 922 065 920 releves sur un canal OFDM), ce qu'un
// int de 32 bits, sur armv7, ne contiendrait pas.
type FlexInt int64

func (f *FlexInt) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		*f = 0
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		if v := ParseNumber(s); v != nil {
			*f = FlexInt(int64(*v))
		}
		return nil
	}
	var v float64
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*f = FlexInt(int64(v))
	return nil
}

// RawDownstream est un canal descendant tel que le modem le decrit.
type RawDownstream struct {
	ChannelID     FlexInt `json:"channelID"`
	Frequency     string  `json:"frequency"`
	Modulation    string  `json:"modulation"`
	PowerLevel    string  `json:"powerLevel"`
	MSE           string  `json:"mse"`
	CorrErrors    FlexInt `json:"corrErrors"`
	NonCorrErrors FlexInt `json:"nonCorrErrors"`
}

// RawUpstream est un canal montant tel que le modem le decrit.
type RawUpstream struct {
	ChannelID  FlexInt `json:"channelID"`
	Frequency  string  `json:"frequency"`
	Modulation string  `json:"modulation"`
	Multiplex  string  `json:"multiplex"`
	PowerLevel string  `json:"powerLevel"`
}

// RawSnapshot est la sortie brute d'un pilote, avant normalisation.
type RawSnapshot struct {
	DOCSIS     string          `json:"docsis"`
	Downstream []RawDownstream `json:"downstream"`
	Upstream   []RawUpstream   `json:"upstream"`
}

// Downstream est un canal descendant normalise.
//
// Les champs numeriques sont des pointeurs : le modem omet certaines valeurs
// selon le type de canal, et un zero silencieux fausserait l'analyse bien plus
// qu'une absence assumee. Un canal OFDM n'a pas de MSE au sens du QAM.
type Downstream struct {
	ChannelID     int
	FrequencyMHz  *float64
	Modulation    string
	PowerDBmV     *float64
	SNRDB         *float64
	CorrErrors    int64
	NonCorrErrors int64
}

// Upstream est un canal montant normalise.
type Upstream struct {
	ChannelID    int
	FrequencyMHz *float64
	Modulation   string
	Multiplex    string
	PowerDBmV    *float64
}

// Snapshot est un releve complet, horodate.
type Snapshot struct {
	Timestamp  time.Time
	DOCSIS     string
	Downstream []Downstream
	Upstream   []Upstream
}

// ParseNumber extrait un nombre d'une valeur du modem.
//
// Accepte "1.0", "-39.0", "650 MHz", "43,5" et les vides. Renvoie nil plutot
// que zero quand la valeur est absente ou illisible : c'est la distinction qui
// evite de compter un canal muet comme un canal a 0 dBmV.
func ParseNumber(raw string) *float64 {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, ",", ".")
	end := 0
	for end < len(s) {
		c := s[end]
		if (c >= '0' && c <= '9') || c == '.' || (end == 0 && (c == '-' || c == '+')) {
			end++
			continue
		}
		break
	}
	value, err := strconv.ParseFloat(strings.TrimSuffix(s[:end], "."), 64)
	if err != nil {
		return nil
	}
	return &value
}

// Normalize convertit un releve brut en releve exploitable.
func (r RawSnapshot) Normalize(at time.Time) Snapshot {
	snap := Snapshot{Timestamp: at, DOCSIS: strings.TrimSpace(r.DOCSIS)}
	for _, c := range r.Downstream {
		snap.Downstream = append(snap.Downstream, Downstream{
			ChannelID:     int(c.ChannelID),
			FrequencyMHz:  ParseNumber(c.Frequency),
			Modulation:    strings.TrimSpace(c.Modulation),
			PowerDBmV:     ParseNumber(c.PowerLevel),
			SNRDB:         snr(c.MSE),
			CorrErrors:    int64(c.CorrErrors),
			NonCorrErrors: int64(c.NonCorrErrors),
		})
	}
	for _, c := range r.Upstream {
		snap.Upstream = append(snap.Upstream, Upstream{
			ChannelID:    int(c.ChannelID),
			FrequencyMHz: ParseNumber(c.Frequency),
			Modulation:   strings.TrimSpace(c.Modulation),
			Multiplex:    strings.TrimSpace(c.Multiplex),
			PowerDBmV:    ParseNumber(c.PowerLevel),
		})
	}
	return snap
}

// snr convertit le MSE du modem en SNR.
//
// Le CGA4233 publie une MSE negative ("-39.0") la ou l'abonne et les seuils
// raisonnent en SNR positif. On prend la valeur absolue plutot que de propager
// deux conventions dans tout le reste du programme.
func snr(mse string) *float64 {
	v := ParseNumber(mse)
	if v == nil {
		return nil
	}
	if *v < 0 {
		positive := -*v
		return &positive
	}
	return v
}

// FlexFloat accepte un nombre ecrit en nombre ou en chaine, avec ou sans unite.
//
// Le firmware melange les deux : "PowerLevel": 1.5 dans une table, "1.5" dans
// une autre, parfois "650 MHz". Un decodage strict rejetterait le releve entier.
type FlexFloat float64

func (f *FlexFloat) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		*f = 0
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		if v := ParseNumber(s); v != nil {
			*f = FlexFloat(*v)
		}
		return nil
	}
	var v float64
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*f = FlexFloat(v)
	return nil
}

// CounterDelta renvoie l'accroissement d'un compteur cumulatif entre deux
// releves.
//
// Un compteur qui baisse a ete remis a zero, en general par un redemarrage du
// modem : sa valeur courante compte alors les erreurs survenues depuis, toutes
// posterieures au releve precedent.
func CounterDelta(prev, cur int64) int64 {
	if cur >= prev {
		return cur - prev
	}
	return cur
}
