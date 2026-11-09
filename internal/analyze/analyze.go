// Package analyze qualifie l'etat d'une ligne a partir des seuils du profil.
//
// Une valeur absente n'est jamais jugee : un canal sans mesure est inconnu,
// pas defaillant.
package analyze

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/operator"
)

// Level qualifie un etat, du meilleur au pire.
type Level int

const (
	Unknown Level = iota
	Good
	Tolerated
	Warning
	Critical
)

func (l Level) String() string {
	switch l {
	case Good:
		return "bon"
	case Tolerated:
		return "tolere"
	case Warning:
		return "avertissement"
	case Critical:
		return "critique"
	default:
		return "inconnu"
	}
}

// worst retient le plus severe des deux, Unknown ne masquant jamais un verdict.
func worst(a, b Level) Level {
	if a > b {
		return a
	}
	return b
}

// Finding explique un verdict en une phrase.
type Finding struct {
	Level   Level
	Metric  string
	Message string
	// Limit dit le seuil applique, en clair : « plage nominale de −8 à
	// +8 dBmV, limites de −15 à +15 dBmV, en 256QAM ».
	Limit string
}

// ChannelHealth est le verdict porte sur un canal.
type ChannelHealth struct {
	ChannelID int
	// FrequencyMHz situe le canal pour un diagnostic : un numero est propre
	// au modem et peut changer, la frequence designe la meme bande pour
	// tout le monde (brouillage 4G a 800 MHz, entree de bruit en bas de la
	// voie montante). Nil si le modem ne la donne pas.
	FrequencyMHz *float64
	Level        Level
	Findings     []Finding
}

// Health est le verdict porte sur un releve complet.
type Health struct {
	Level      Level
	Downstream []ChannelHealth
	Upstream   []ChannelHealth
	Qualified  bool // false si le profil n'est pas calibre
}

// Options porte les declarations de l'abonne sur son installation.
type Options struct {
	// OFDMAFloor est la modulation habituelle des canaux OFDMA du segment (16
	// pour 16QAM) ; 0 garde les seuils du profil. Un cran dessous est a
	// surveiller, deux sont critiques.
	OFDMAFloor int
	// Errors compte, par canal descendant, les erreurs survenues sur la fenetre
	// du profil (ErrorLimits.Window). Sans elles, les erreurs ne sont pas jugees.
	Errors map[int]ErrorCount
}

// ErrorCount compte les erreurs d'un canal sur la fenetre de jugement.
type ErrorCount struct {
	Corrected   int64
	Uncorrected int64
}

// Snapshot analyse un releve avec les options par defaut.
func Snapshot(snap docsis.Snapshot, profile operator.Profile) Health {
	return SnapshotWith(snap, profile, Options{})
}

// SnapshotWith analyse un releve.
//
// Un profil non calibre ne produit aucun verdict.
func SnapshotWith(snap docsis.Snapshot, profile operator.Profile, opts Options) Health {
	h := Health{Qualified: profile.Calibrated}
	if !profile.Calibrated {
		return h
	}
	t := Effective(profile.Thresholds, opts)
	ref, hasRef := scqamSlope(snap.Downstream)
	for _, c := range snap.Downstream {
		ch := downstream(c, t, opts.Errors, ref, hasRef)
		h.Downstream = append(h.Downstream, ch)
		h.Level = worst(h.Level, ch.Level)
	}
	for _, c := range snap.Upstream {
		ch := upstream(c, t)
		h.Upstream = append(h.Upstream, ch)
		h.Level = worst(h.Level, ch.Level)
	}
	return h
}

// found construit un constat ; son seuil est renseigne par l'appelant.
func found(l Level, metric, msg string, limit ...string) Finding {
	f := Finding{Level: l, Metric: metric, Message: msg}
	if len(limit) > 0 {
		f.Limit = limit[0]
	}
	return f
}

// Effective renvoie les seuils appliques, apres les declarations de
// l'abonne.
func Effective(t operator.Thresholds, opts Options) operator.Thresholds {
	if f := opts.OFDMAFloor; f > 0 {
		t.UpstreamOFDMA = operator.ModulationLimits{CriticalMaxQAM: f / 4, WarningMaxQAM: f / 2, Usual: f}
	}
	return t
}

// DownstreamMetric rejuge une seule mesure d'un canal descendant, sans les
// erreurs ni la vraisemblance, qui demandent le releve entier : de quoi
// dater le debut d'un constat dans l'historique.
func DownstreamMetric(c docsis.Downstream, t operator.Thresholds, metric string) (Level, bool) {
	return levelOf(downstream(c, t, nil, slope{}, false), metric)
}

// UpstreamMetric rejuge une seule mesure d'un canal montant.
func UpstreamMetric(c docsis.Upstream, t operator.Thresholds, metric string) (Level, bool) {
	return levelOf(upstream(c, t), metric)
}

func levelOf(ch ChannelHealth, metric string) (Level, bool) {
	for _, f := range ch.Findings {
		if f.Metric == metric {
			return f.Level, true
		}
	}
	return Unknown, false
}

func downstream(c docsis.Downstream, t operator.Thresholds, errs map[int]ErrorCount, ref slope, hasRef bool) ChannelHealth {
	out := ChannelHealth{ChannelID: c.ChannelID, FrequencyMHz: c.FrequencyMHz}

	if bands, ok := t.DownstreamPowerFor(c.Modulation); ok && c.PowerDBmV != nil {
		f := power(*c.PowerDBmV, bands, "puissance descendante")
		f.Limit = powerLimit(bands, t.DownstreamPowerKey(c.Modulation))
		if doubt, ok := implausibleOFDMPower(c, f, ref, hasRef, t); ok {
			f = doubt
		}
		out.Findings = append(out.Findings, f)
		out.Level = worst(out.Level, f.Level)
	}
	if bands, ok := t.SNRFor(c.Modulation); ok && c.SNRDB != nil {
		f := snr(*c.SNRDB, bands)
		f.Limit = snrLimit(bands, t.SNRKey(c.Modulation))
		out.Findings = append(out.Findings, f)
		out.Level = worst(out.Level, f.Level)
	}
	if e, known := errs[c.ChannelID]; known {
		f := errors(e, t.Errors)
		f.Limit = errorLimit(t.Errors)
		out.Findings = append(out.Findings, f)
		out.Level = worst(out.Level, f.Level)
	}
	return out
}

func upstream(c docsis.Upstream, t operator.Thresholds) ChannelHealth {
	out := ChannelHealth{ChannelID: c.ChannelID, FrequencyMHz: c.FrequencyMHz}

	family := kind(c.Modulation)
	if family != "ofdma" && kind(c.Multiplex) == "ofdma" {
		family = "ofdma"
	}
	if bands, ok := t.UpstreamPowerFor(family); ok && c.PowerDBmV != nil {
		f := power(*c.PowerDBmV, bands, "puissance montante")
		f.Limit = powerLimit(bands, t.UpstreamPowerKey(family))
		out.Findings = append(out.Findings, f)
		out.Level = worst(out.Level, f.Level)
	}
	limits := t.UpstreamModulation
	label := c.Modulation
	if kind(c.Modulation) == "ofdma" || kind(c.Multiplex) == "ofdma" {
		limits = t.UpstreamOFDMA
		// Un canal OFDMA porte "OFDMA" en modulation ; son ordre reel est
		// celui du profil actif, que le pilote range dans Multiplex.
		if qamOrder(c.Multiplex) > 0 {
			label = c.Multiplex
		}
	}
	if f, ok := modulation(label, limits); ok {
		f.Limit = modulationLimit(limits)
		out.Findings = append(out.Findings, f)
		out.Level = worst(out.Level, f.Level)
	}
	return out
}

// power qualifie une puissance par intervalles emboites.
func power(v float64, b operator.PowerBands, label string) Finding {
	switch {
	case b.Good.Contains(v):
		return found(Good, label, fmt.Sprintf("%s\u00a0dBmV, dans la plage nominale", num(v)))
	case b.Warning.Contains(v):
		return found(Warning, label, fmt.Sprintf("%s\u00a0dBmV, hors de la plage nominale", num(v)))
	case b.Critical.Contains(v):
		return found(Critical, label, fmt.Sprintf("%s\u00a0dBmV, proche des limites", num(v)))
	default:
		return found(Critical, label, fmt.Sprintf("%s\u00a0dBmV, hors limites", num(v)))
	}
}

func snr(v float64, b operator.SNRBands) Finding {
	switch {
	case v >= b.GoodMin:
		return found(Good, "SNR", fmt.Sprintf("%s\u00a0dB", num(v)))
	case v >= b.WarningMin:
		return found(Warning, "SNR", fmt.Sprintf("%s\u00a0dB, sous le seuil nominal", num(v)))
	case v >= b.CriticalMin:
		return found(Critical, "SNR", fmt.Sprintf("%s\u00a0dB, dégradé", num(v)))
	default:
		return found(Critical, "SNR", fmt.Sprintf("%s\u00a0dB, très dégradé", num(v)))
	}
}

// errors qualifie les erreurs non corrigees sur la fenetre.
//
// Le modem ne publie pas le nombre de blocs recus : la proportion porte sur
// les erreurs. Un verdict defavorable exige aussi un volume d'erreurs non
// corrigees au moins egal a ErrorLimits.Spike, et un volume minimal
// d'erreurs pour que la proportion compte.
func errors(e ErrorCount, limits operator.ErrorLimits) Finding {
	window := hours(limits.Window())
	total := e.Corrected + e.Uncorrected
	switch {
	case total == 0:
		return found(Good, "erreurs", "aucune erreur sur "+window)
	case e.Uncorrected == 0:
		return found(Good, "erreurs", fmt.Sprintf("%s corrigées sur %s, aucune perdue", count(total), window))
	case total < limits.MinVolume():
		return found(Good, "erreurs", fmt.Sprintf("%s sur %s, dont %s non corrigées",
			count(total), window, count(e.Uncorrected)))
	}
	pct := float64(e.Uncorrected) / float64(total) * 100
	msg := fmt.Sprintf("%s non corrigées sur %s en %s (%s\u202f%%)",
		count(e.Uncorrected), count(total), window, num(pct))
	switch {
	case e.Uncorrected < limits.Spike():
		return found(Good, "erreurs", msg)
	case pct >= limits.UncorrectablePctCritical:
		return found(Critical, "erreurs", msg)
	case pct >= limits.UncorrectablePctWarning:
		return found(Warning, "erreurs", msg)
	default:
		return found(Good, "erreurs", msg)
	}
}

// hours rend une fenetre en heures : « 24 h ».
func hours(d time.Duration) string {
	return strconv.Itoa(int(d.Hours())) + "\u00a0h"
}

// count rend un entier a la francaise : 12 400.
func count(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "\u202f" + s[i:]
	}
	return s
}

// powerLimit decrit des bandes de puissance.
func powerLimit(b operator.PowerBands, key string) string {
	return "plage nominale " + span(b.Good) + "\u00a0dBmV, limites " + span(b.Critical) + "\u00a0dBmV" + keyText(key)
}

// snrLimit decrit des planchers de SNR.
func snrLimit(b operator.SNRBands, key string) string {
	return "nominal dès " + short(b.GoodMin) + "\u00a0dB, critique sous " + short(b.WarningMin) + "\u00a0dB" + keyText(key)
}

func errorLimit(e operator.ErrorLimits) string {
	return "à surveiller dès " + short(e.UncorrectablePctWarning) + "\u202f% d'erreurs non corrigées, critique dès " +
		short(e.UncorrectablePctCritical) + "\u202f%, à partir de " + count(e.Spike()) + " non corrigées sur " + hours(e.Window())
}

func modulationLimit(l operator.ModulationLimits) string {
	s := ""
	if l.Usual > 0 {
		s = "modulation habituelle déclarée : " + strconv.Itoa(l.Usual) + "QAM ; "
	}
	s += "à surveiller à " + strconv.Itoa(l.WarningMaxQAM) + "QAM ou moins, critique à " + strconv.Itoa(l.CriticalMaxQAM) + "QAM ou moins"
	if l.Usual == 0 && l.ToleratedMaxQAM > 0 {
		s += ", tolérée jusqu'à " + strconv.Itoa(l.ToleratedMaxQAM) + "QAM"
	}
	return s
}

// keyText dit pour quelle modulation le seuil est defini : « , en 256QAM ».
func keyText(key string) string {
	switch key {
	case "":
		return ""
	case "ofdm":
		return ", en OFDM"
	case "ofdma":
		return ", en OFDMA"
	case "sc_qam":
		return ", en SC-QAM"
	}
	return ", en " + key
}

// span rend un intervalle : « de −8 à +8 », « de 28 à 49 ».
func span(r operator.Range) string {
	hi := short(r.Max)
	if r.Min < 0 && r.Max > 0 {
		hi = "+" + hi
	}
	return "de " + short(r.Min) + " à " + hi
}

// short rend un seuil sans decimale inutile : 8, 25,5, −8.
func short(v float64) string {
	s := num(v)
	s = strings.TrimSuffix(s, ",0")
	return strings.Replace(s, "-", "\u2212", 1)
}

func modulation(m string, limits operator.ModulationLimits) (Finding, bool) {
	qam := qamOrder(m)
	if qam == 0 {
		return Finding{}, false
	}
	if u := limits.Usual; u > 0 {
		switch {
		case qam <= limits.CriticalMaxQAM:
			return found(Critical, "modulation montante",
				fmt.Sprintf("%s, très en dessous de la modulation habituelle du segment (%dQAM)", m, u)), true
		case qam <= limits.WarningMaxQAM:
			return found(Warning, "modulation montante",
				fmt.Sprintf("%s, sous la modulation habituelle du segment (%dQAM)", m, u)), true
		case qam == u:
			return found(Good, "modulation montante", m+", modulation habituelle du segment"), true
		default:
			return found(Good, "modulation montante", m), true
		}
	}
	switch {
	case qam <= limits.CriticalMaxQAM:
		return found(Critical, "modulation montante",
			fmt.Sprintf("%s, très basse", m)), true
	case qam <= limits.WarningMaxQAM:
		return found(Warning, "modulation montante",
			fmt.Sprintf("%s, basse : comparer avec l'historique avant de conclure", m)), true
	case limits.ToleratedMaxQAM > 0 && qam <= limits.ToleratedMaxQAM:
		return found(Tolerated, "modulation montante", m), true
	default:
		return found(Good, "modulation montante", m), true
	}
}

// qamOrder extrait l'ordre de modulation : "64QAM" donne 64.
func qamOrder(m string) int {
	s := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(m), " ", ""))
	s = strings.TrimSuffix(s, "QAM")
	s = strings.TrimPrefix(s, "QAM")
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func kind(m string) string {
	if strings.Contains(strings.ToLower(m), "ofdma") {
		return "ofdma"
	}
	return "sc_qam"
}

// num rend un nombre a une decimale, a la francaise : 1,5 et non 1.5.
func num(v float64) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', 1, 64), ".", ",", 1)
}

// QAMOrder extrait l'ordre d'une modulation : 64 pour « 64QAM », 0 si la
// chaine n'en porte pas (« OFDM », vide).
func QAMOrder(m string) int { return qamOrder(m) }
