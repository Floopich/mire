package analyze

import (
	"fmt"
	"strings"

	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/operator"
)

// Puissance OFDM invraisemblable.
//
// DOCSIS 3.1 exprime la puissance d'un canal OFDM ramenee a 6 MHz : elle se
// compare donc a celle des canaux SC-QAM. Sur un cable, la puissance suit la
// frequence presque en ligne droite (la pente du reseau) ; le canal OFDM se
// place sur le prolongement de celle de ses voisins SC-QAM.
//
// Certains firmwares publient pourtant, apres plusieurs jours de
// fonctionnement, une puissance OFDM sans rapport avec ce qu'ils recoivent.
// Cas releve sur un CGA4233 VOO : 26,6 dBmV figes pendant plus d'un jour,
// pour 7 dBmV attendus et une reception excellente (SNR 43,5 dB) ; 6,2 dBmV
// apres un redemarrage du modem. Un signal reellement 20 dB trop fort sur un
// seul canal, sans effet sur sa qualite, n'est pas physique.
//
// Quand l'ecart a la pente depasse implausibleGap et que le reste du canal
// est sain, la puissance est donc signalee comme une mesure douteuse, a
// surveiller, et non comme une ligne hors limites. Les conditions sont
// cumulatives : au moindre doute, le verdict ordinaire s'applique.

const (
	// implausibleGap est l'ecart a la pente au-dela duquel la mesure est
	// douteuse. La pente extrapolee sur une centaine de MHz se trompe de
	// quelques dB au plus ; 10 dB laisse une large marge.
	implausibleGap = 10.0
	// minSCQAM est le nombre de canaux SC-QAM necessaires pour tracer la pente.
	minSCQAM = 4
)

// slope est la droite puissance = a + b x frequence des canaux SC-QAM.
type slope struct{ a, b float64 }

func (s slope) at(mhz float64) float64 { return s.a + s.b*mhz }

// scqamSlope ajuste la pente par les moindres carres.
func scqamSlope(ds []docsis.Downstream) (slope, bool) {
	var n, sx, sy, sxx, sxy float64
	for _, c := range ds {
		if isOFDM(c.Modulation) || c.FrequencyMHz == nil || c.PowerDBmV == nil {
			continue
		}
		x, y := *c.FrequencyMHz, *c.PowerDBmV
		n++
		sx += x
		sy += y
		sxx += x * x
		sxy += x * y
	}
	den := n*sxx - sx*sx
	if n < minSCQAM || den == 0 {
		return slope{}, false
	}
	b := (n*sxy - sx*sy) / den
	return slope{a: (sy - b*sx) / n, b: b}, true
}

// implausibleOFDMPower renvoie le constat qui remplace un verdict de
// puissance defavorable quand la mesure du canal OFDM est invraisemblable.
func implausibleOFDMPower(c docsis.Downstream, power Finding, ref slope, hasRef bool, t operator.Thresholds) (Finding, bool) {
	if !hasRef || !isOFDM(c.Modulation) || power.Level < Warning ||
		c.PowerDBmV == nil || c.FrequencyMHz == nil || c.SNRDB == nil {
		return Finding{}, false
	}
	bands, ok := t.DownstreamPowerFor(c.Modulation)
	snrBands, okSNR := t.SNRFor(c.Modulation)
	expected := ref.at(*c.FrequencyMHz)
	gap := *c.PowerDBmV - expected
	switch {
	case !ok || !okSNR:
		return Finding{}, false
	case !bands.Good.Contains(expected): // les voisins eux-memes sont hors plage
		return Finding{}, false
	case *c.SNRDB < snrBands.GoodMin: // la reception souffre : le verdict tient
		return Finding{}, false
	case gap < implausibleGap && gap > -implausibleGap:
		return Finding{}, false
	}
	return Finding{Warning, "puissance descendante", fmt.Sprintf(
		"%s dBmV publiés pour %s attendus d'après les canaux SC-QAM, avec une réception excellente : "+
			"mesure probablement erronée du modem, à vérifier en le redémarrant",
		num(*c.PowerDBmV), num(expected))}, true
}

func isOFDM(m string) bool {
	m = strings.ToLower(strings.TrimSpace(m))
	return m == "ofdm"
}
