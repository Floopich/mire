package modem

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/floopich/mire/internal/docsis"
)

// payload est la reponse de /api/v1/modem/... du CGA4233.
//
// Le firmware repond tantot {"data": {...}}, tantot les tables a la racine :
// les deux formes sont acceptees.
type payload struct {
	Data *tables `json:"data"`
	tables
}

type tables struct {
	DSTbl   []dsSCQAM `json:"DSTbl"`
	ExDSTbl []dsOFDM  `json:"exDSTbl"`
	USTbl   []usSCQAM `json:"USTbl"`
	ExUSTbl []usOFDMA `json:"exUSTbl"`
	ErrTbl  []errRow  `json:"ErrTbl"`
}

type dsSCQAM struct {
	ChannelID  docsis.FlexFloat `json:"ChannelID"`
	Modulation string           `json:"Modulation"`
	Frequency  docsis.FlexFloat `json:"Frequency"`
	PowerLevel docsis.FlexFloat `json:"PowerLevel"`
	SNRLevel   docsis.FlexFloat `json:"SNRLevel"`
	LockStatus string           `json:"LockStatus"`
	errRow
}

type dsOFDM struct {
	ChannelID        docsis.FlexFloat `json:"ChannelID"`
	CentralFrequency docsis.FlexFloat `json:"CentralFrequency"`
	PowerLevel       docsis.FlexFloat `json:"PowerLevel"`
	SNRLevel         docsis.FlexFloat `json:"SNRLevel"`
	FFT              string           `json:"FFT"`
	LockStatus       string           `json:"LockStatus"`
}

type usSCQAM struct {
	ChannelID  docsis.FlexFloat `json:"ChannelID"`
	Modulation string           `json:"Modulation"`
	Frequency  docsis.FlexFloat `json:"Frequency"`
	PowerLevel docsis.FlexFloat `json:"PowerLevel"`
	LockStatus string           `json:"LockStatus"`
}

type usOFDMA struct {
	ChannelID        docsis.FlexFloat `json:"ChannelID"`
	CentralFrequency docsis.FlexFloat `json:"CentralFrequency"`
	PowerLevel       docsis.FlexFloat `json:"PowerLevel"`
	FFT              string           `json:"FFT"`
	LockStatus       string           `json:"LockStatus"`
}

type errRow struct {
	Correcteds     docsis.FlexFloat `json:"Correcteds"`
	Uncorrectables docsis.FlexFloat `json:"Uncorrectables"`
}

// parsePayload convertit une reponse du modem en releve brut.
func parsePayload(raw []byte) (docsis.RawSnapshot, error) {
	var p payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return docsis.RawSnapshot{}, fmt.Errorf("réponse illisible : %w", err)
	}
	t := p.tables
	if p.Data != nil {
		t = *p.Data
	}

	snap := docsis.RawSnapshot{DOCSIS: "3.1"}

	for _, c := range t.DSTbl {
		if !isChannel(c.ChannelID, c.Frequency, c.LockStatus) {
			continue
		}
		snap.Downstream = append(snap.Downstream, docsis.RawDownstream{
			ChannelID:     docsis.FlexInt(c.ChannelID),
			Frequency:     mhz(c.Frequency),
			Modulation:    normalizeModulation(c.Modulation),
			PowerLevel:    number(c.PowerLevel),
			MSE:           mse(c.SNRLevel),
			CorrErrors:    docsis.FlexInt(c.Correcteds),
			NonCorrErrors: docsis.FlexInt(c.Uncorrectables),
		})
	}

	// ErrTbl est positionnel : les premieres lignes doublent DSTbl, les
	// suivantes portent les compteurs des canaux OFDM, qui n'ont pas de
	// champ d'erreur propre. Decaler cet index fausserait silencieusement
	// les compteurs de tous les canaux OFDM.
	ofdmErrors := []errRow{}
	if len(t.ErrTbl) > len(t.DSTbl) {
		ofdmErrors = t.ErrTbl[len(t.DSTbl):]
	}
	for i, c := range t.ExDSTbl {
		if !isChannel(c.ChannelID, c.CentralFrequency, c.LockStatus) {
			continue
		}
		var e errRow
		if i < len(ofdmErrors) {
			e = ofdmErrors[i]
		}
		snap.Downstream = append(snap.Downstream, docsis.RawDownstream{
			ChannelID:     docsis.FlexInt(c.ChannelID),
			Frequency:     mhz(c.CentralFrequency),
			Modulation:    "OFDM",
			PowerLevel:    number(c.PowerLevel),
			MSE:           mse(c.SNRLevel),
			CorrErrors:    docsis.FlexInt(e.Correcteds),
			NonCorrErrors: docsis.FlexInt(e.Uncorrectables),
		})
	}

	for _, c := range t.USTbl {
		if !isChannel(c.ChannelID, c.Frequency, c.LockStatus) {
			continue
		}
		snap.Upstream = append(snap.Upstream, docsis.RawUpstream{
			ChannelID:  docsis.FlexInt(c.ChannelID),
			Frequency:  mhz(c.Frequency),
			Modulation: normalizeModulation(c.Modulation),
			PowerLevel: number(c.PowerLevel),
		})
	}
	for _, c := range t.ExUSTbl {
		if !isChannel(c.ChannelID, c.CentralFrequency, c.LockStatus) {
			continue
		}
		snap.Upstream = append(snap.Upstream, docsis.RawUpstream{
			ChannelID:  docsis.FlexInt(c.ChannelID),
			Frequency:  mhz(c.CentralFrequency),
			Modulation: "OFDMA",
			Multiplex:  strings.TrimSpace(c.FFT),
			PowerLevel: number(c.PowerLevel),
		})
	}
	return snap, nil
}

// isChannel distingue un canal d'un emplacement de recepteur.
//
// Le modem publie une ligne par recepteur, meme sans canal : identifiant nul
// ou aberrant (1293955120), champs vides. Un canal a un identifiant DOCSIS,
// sur 8 bits et jamais nul, une frequence, et il est accroche quand le modem
// en publie l'etat. Un canal decroche disparait donc du releve : le journal
// le signale comme perdu.
func isChannel(id, frequency docsis.FlexFloat, lock string) bool {
	if id < 1 || id > 255 || frequency <= 0 {
		return false
	}
	lock = strings.TrimSpace(lock)
	return lock == "" || strings.EqualFold(lock, "Locked")
}

// mhz rend une frequence en MHz, au dixieme.
//
// Le firmware VOO publie des MHz (« 37.2 MHz », « 706.487488 MHz ») ;
// d'autres versions publient des Hz (650000000). Aucune frequence DOCSIS ne
// depasse 2 GHz : au-dela de 10 000, la valeur est forcement en Hz.
func mhz(v docsis.FlexFloat) string {
	f := float64(v)
	if f == 0 {
		return ""
	}
	if f > 10_000 {
		f /= 1e6
	}
	return strconv.FormatFloat(math.Round(f*10)/10, 'f', -1, 64) + " MHz"
}

func number(v docsis.FlexFloat) string {
	return fmt.Sprintf("%g", float64(v))
}

// mse reproduit la convention du firmware : MSE negative, SNR positif.
//
// Le modem publie un SNR positif ; le modele attend une MSE, et la
// normalisation en reprend la valeur absolue. On rend donc la valeur negative
// ici plutot que de creer une deuxieme convention.
func mse(snr docsis.FlexFloat) string {
	v := math.Abs(float64(snr))
	if v == 0 {
		return ""
	}
	return fmt.Sprintf("%g", -v)
}

// normalizeModulation ramene "256qam", "256 QAM" ou "qam256" a "256QAM".
func normalizeModulation(s string) string {
	t := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	t = strings.ReplaceAll(t, "-", "")
	if strings.HasPrefix(t, "QAM") && len(t) > 3 {
		t = t[3:] + "QAM"
	}
	return t
}
