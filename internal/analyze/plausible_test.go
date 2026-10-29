package analyze

import (
	"fmt"
	"strings"
	"testing"

	"github.com/floopich/mire/internal/docsis"
)

// ligneVOO reprend le releve reel du 26 octobre 2026 : seize SC-QAM de 442 a
// 570 MHz, et l'OFDM 159 a 706,5 MHz avec la puissance et le SNR donnes.
func ligneVOO(ofdmPower, ofdmSNR string, scqam int) docsis.Snapshot {
	freq := []int{442, 450, 458, 466, 482, 490, 498, 506, 514, 522, 530, 538, 546, 554, 562, 570}
	pw := []string{"2.1", "2.6", "3.0", "3.3", "3.4", "3.5", "3.8", "4.1", "4.3", "4.2", "4.2", "4.0", "4.1", "4.3", "4.5", "4.6"}
	var ds []docsis.RawDownstream
	for i := range scqam {
		ds = append(ds, docsis.RawDownstream{ChannelID: docsis.FlexInt(i + 1), Modulation: "256QAM",
			Frequency: fmt.Sprintf("%d MHz", freq[i]), PowerLevel: pw[i], MSE: "-40.2"})
	}
	ds = append(ds, docsis.RawDownstream{ChannelID: 159, Modulation: "OFDM", Frequency: "706.5 MHz",
		PowerLevel: ofdmPower, MSE: "-" + ofdmSNR})
	return releve(ds, nil)
}

func ofdmPower(t *testing.T, h Health) Finding {
	t.Helper()
	for _, c := range h.Downstream {
		if c.ChannelID == 159 {
			for _, f := range c.Findings {
				if f.Metric == "puissance descendante" {
					return f
				}
			}
		}
	}
	t.Fatal("aucun constat de puissance pour l'OFDM 159")
	return Finding{}
}

func TestPuissanceOFDMInvraisemblableEstUneMesureDouteuse(t *testing.T) {
	// Le cas reel : 26,6 dBmV publies, 7 attendus, reception excellente.
	h := Snapshot(ligneVOO("26.6", "43.5", 16), profilVOO(t))
	f := ofdmPower(t, h)
	if f.Level != Warning || !strings.Contains(f.Message, "mesure probablement erronée") {
		t.Fatalf("constat %v : %s", f.Level, f.Message)
	}
	if !strings.Contains(f.Message, "attendus d'après les canaux SC-QAM") || h.Level != Warning {
		t.Errorf("ligne %v, message %q", h.Level, f.Message)
	}
	// Apres le redemarrage du modem : 6,2 dBmV, rien a signaler.
	if f := ofdmPower(t, Snapshot(ligneVOO("6.2", "43.5", 16), profilVOO(t))); f.Level != Good {
		t.Errorf("6,2 dBmV : %v, attendu bon", f.Level)
	}
}

func TestLeVerdictOrdinaireTientAuMoindreDoute(t *testing.T) {
	cas := []struct {
		nom, power, snr string
		scqam           int
	}{
		{"réception dégradée", "26.6", "26.0", 16},
		{"trop peu de SC-QAM pour tracer la pente", "26.6", "43.5", 3},
		{"écart faible à la pente", "16.0", "43.5", 16},
	}
	for _, c := range cas {
		f := ofdmPower(t, Snapshot(ligneVOO(c.power, c.snr, c.scqam), profilVOO(t)))
		if f.Level != Critical || strings.Contains(f.Message, "erronée") {
			t.Errorf("%s : %v, %q ; attendu le verdict critique ordinaire", c.nom, f.Level, f.Message)
		}
	}
}

func TestPenteSCQAM(t *testing.T) {
	ref, ok := scqamSlope(ligneVOO("6.2", "43.5", 16).Downstream)
	if !ok {
		t.Fatal("pente non tracee")
	}
	// Prolongee a 706,5 MHz, la pente reelle donne un peu plus de 7 dBmV.
	if v := ref.at(706.5); v < 6.5 || v > 7.5 {
		t.Errorf("puissance attendue %.2f dBmV, attendu environ 7", v)
	}
}
