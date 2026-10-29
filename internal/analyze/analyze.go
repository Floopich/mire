// Package analyze qualifie l'etat d'une ligne a partir des seuils du profil.
//
// L'analyse ne juge jamais une valeur absente : un canal sans mesure est
// inconnu, pas defaillant. C'est la distinction qui evite qu'un modem en cours
// de synchronisation soit rapporte comme une panne.
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
}

// ChannelHealth est le verdict porte sur un canal.
type ChannelHealth struct {
	ChannelID int
	Level     Level
	Findings  []Finding
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
	// OFDMAFloor est la modulation habituelle des canaux OFDMA du segment,
	// declaree par l'abonne (16 pour 16QAM) ; 0 garde les seuils du profil.
	// Certains segments fonctionnent durablement en basse modulation : sans
	// cette declaration, l'abonne verrait une alerte permanente qu'aucune
	// intervention ne ferait disparaitre. Les variations restent jugees par
	// rapport a ce plancher : un cran dessous est a surveiller, deux sont
	// critiques.
	OFDMAFloor int
	// Errors compte, par canal descendant, les erreurs survenues sur la
	// fenetre du profil (ErrorLimits.Window). Sans elles, les erreurs ne sont
	// pas jugees : les compteurs bruts du modem cumulent depuis son demarrage
	// et ne disent rien de l'etat present.
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
// Un profil non calibre ne produit aucun verdict : appliquer les tolerances
// d'un autre reseau donnerait un resultat faux avec l'apparence du serieux.
func SnapshotWith(snap docsis.Snapshot, profile operator.Profile, opts Options) Health {
	h := Health{Qualified: profile.Calibrated}
	if !profile.Calibrated {
		return h
	}
	t := profile.Thresholds
	if f := opts.OFDMAFloor; f > 0 {
		t.UpstreamOFDMA = operator.ModulationLimits{CriticalMaxQAM: f / 4, WarningMaxQAM: f / 2, Usual: f}
	}
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

func downstream(c docsis.Downstream, t operator.Thresholds, errs map[int]ErrorCount, ref slope, hasRef bool) ChannelHealth {
	out := ChannelHealth{ChannelID: c.ChannelID}

	if bands, ok := t.DownstreamPowerFor(c.Modulation); ok && c.PowerDBmV != nil {
		f := power(*c.PowerDBmV, bands, "puissance descendante")
		if doubt, ok := implausibleOFDMPower(c, f, ref, hasRef, t); ok {
			f = doubt
		}
		out.Findings = append(out.Findings, f)
		out.Level = worst(out.Level, f.Level)
	}
	if bands, ok := t.SNRFor(c.Modulation); ok && c.SNRDB != nil {
		f := snr(*c.SNRDB, bands)
		out.Findings = append(out.Findings, f)
		out.Level = worst(out.Level, f.Level)
	}
	if e, known := errs[c.ChannelID]; known {
		f := errors(e, t.Errors)
		out.Findings = append(out.Findings, f)
		out.Level = worst(out.Level, f.Level)
	}
	return out
}

func upstream(c docsis.Upstream, t operator.Thresholds) ChannelHealth {
	out := ChannelHealth{ChannelID: c.ChannelID}

	family := kind(c.Modulation)
	if family != "ofdma" && kind(c.Multiplex) == "ofdma" {
		family = "ofdma"
	}
	if bands, ok := t.UpstreamPowerFor(family); ok && c.PowerDBmV != nil {
		f := power(*c.PowerDBmV, bands, "puissance montante")
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
		out.Findings = append(out.Findings, f)
		out.Level = worst(out.Level, f.Level)
	}
	return out
}

// power qualifie une puissance par intervalles emboites.
func power(v float64, b operator.PowerBands, label string) Finding {
	switch {
	case b.Good.Contains(v):
		return Finding{Good, label, fmt.Sprintf("%s\u00a0dBmV, dans la plage nominale", num(v))}
	case b.Warning.Contains(v):
		return Finding{Warning, label, fmt.Sprintf("%s\u00a0dBmV, hors de la plage nominale", num(v))}
	case b.Critical.Contains(v):
		return Finding{Critical, label, fmt.Sprintf("%s\u00a0dBmV, proche des limites", num(v))}
	default:
		return Finding{Critical, label, fmt.Sprintf("%s\u00a0dBmV, hors limites", num(v))}
	}
}

func snr(v float64, b operator.SNRBands) Finding {
	switch {
	case v >= b.GoodMin:
		return Finding{Good, "SNR", fmt.Sprintf("%s\u00a0dB", num(v))}
	case v >= b.WarningMin:
		return Finding{Warning, "SNR", fmt.Sprintf("%s\u00a0dB, sous le seuil nominal", num(v))}
	case v >= b.CriticalMin:
		return Finding{Critical, "SNR", fmt.Sprintf("%s\u00a0dB, dégradé", num(v))}
	default:
		return Finding{Critical, "SNR", fmt.Sprintf("%s\u00a0dB, très dégradé", num(v))}
	}
}

// errors qualifie les erreurs non corrigees sur la fenetre.
//
// Le modem ne publie pas le nombre de blocs recus : la proportion porte donc
// sur les erreurs, pas sur les donnees transmises. Sur une ligne propre, peu
// d'erreurs dont quelques dizaines non corrigees donnent un pourcentage
// eleve pour une perte imperceptible. Un verdict defavorable exige donc aussi
// un volume : au moins autant d'erreurs non corrigees sur la fenetre que le
// profil en juge un pic entre deux releves (ErrorLimits.Spike).
//
// Sous un volume minimal d'erreurs, la proportion ne signifie rien non plus :
// trois erreurs dont une non corrigee ne font pas 33 % de pertes.
func errors(e ErrorCount, limits operator.ErrorLimits) Finding {
	window := hours(limits.Window())
	total := e.Corrected + e.Uncorrected
	switch {
	case total == 0:
		return Finding{Good, "erreurs", "aucune erreur sur " + window}
	case e.Uncorrected == 0:
		return Finding{Good, "erreurs", fmt.Sprintf("%s corrigées sur %s, aucune perdue", count(total), window)}
	case total < limits.MinVolume():
		return Finding{Good, "erreurs", fmt.Sprintf("%s sur %s, dont %s non corrigées",
			count(total), window, count(e.Uncorrected))}
	}
	pct := float64(e.Uncorrected) / float64(total) * 100
	msg := fmt.Sprintf("%s non corrigées sur %s erreurs en %s (%s\u202f%%)",
		count(e.Uncorrected), count(total), window, num(pct))
	switch {
	case e.Uncorrected < limits.Spike():
		return Finding{Good, "erreurs", msg}
	case pct >= limits.UncorrectablePctCritical:
		return Finding{Critical, "erreurs", msg}
	case pct >= limits.UncorrectablePctWarning:
		return Finding{Warning, "erreurs", msg}
	default:
		return Finding{Good, "erreurs", msg}
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

// modulation qualifie la modulation montante.
//
// Un canal durablement bas peut relever de la configuration du segment plutot
// que d'une degradation : le verdict signale, il ne conclut pas.
func modulation(m string, limits operator.ModulationLimits) (Finding, bool) {
	qam := qamOrder(m)
	if qam == 0 {
		return Finding{}, false
	}
	if u := limits.Usual; u > 0 {
		switch {
		case qam <= limits.CriticalMaxQAM:
			return Finding{Critical, "modulation montante",
				fmt.Sprintf("%s, très en dessous de la modulation habituelle du segment (%dQAM)", m, u)}, true
		case qam <= limits.WarningMaxQAM:
			return Finding{Warning, "modulation montante",
				fmt.Sprintf("%s, sous la modulation habituelle du segment (%dQAM)", m, u)}, true
		case qam == u:
			return Finding{Good, "modulation montante", m + ", modulation habituelle du segment"}, true
		default:
			return Finding{Good, "modulation montante", m}, true
		}
	}
	switch {
	case qam <= limits.CriticalMaxQAM:
		return Finding{Critical, "modulation montante",
			fmt.Sprintf("%s, très basse", m)}, true
	case qam <= limits.WarningMaxQAM:
		return Finding{Warning, "modulation montante",
			fmt.Sprintf("%s, basse : comparer avec l'historique avant de conclure", m)}, true
	case limits.ToleratedMaxQAM > 0 && qam <= limits.ToleratedMaxQAM:
		return Finding{Tolerated, "modulation montante", m}, true
	default:
		return Finding{Good, "modulation montante", m}, true
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

// kind distingue les familles de canaux montants.
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
