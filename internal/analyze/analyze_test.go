package analyze

import (
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/operator"
)

func profilVOO(t *testing.T) operator.Profile {
	t.Helper()
	p, err := operator.Load("voo")
	if err != nil {
		t.Fatalf("profil: %v", err)
	}
	return p
}

func releve(ds []docsis.RawDownstream, us []docsis.RawUpstream) docsis.Snapshot {
	return docsis.RawSnapshot{DOCSIS: "3.1", Downstream: ds, Upstream: us}.
		Normalize(time.Now())
}

func TestLigneSaineEstBonne(t *testing.T) {
	// Les valeurs relevees sur une ligne VOO en bon etat.
	h := Snapshot(releve(
		[]docsis.RawDownstream{{ChannelID: 1, Modulation: "256QAM",
			PowerLevel: "3.8", MSE: "-41.2"}},
		[]docsis.RawUpstream{{ChannelID: 5, Modulation: "64QAM", PowerLevel: "45.5"}},
	), profilVOO(t))

	if h.Level != Good {
		t.Errorf("verdict %s, attendu bon : %+v", h.Level, h.Downstream[0].Findings)
	}
	if !h.Qualified {
		t.Error("le profil VOO est calibre, l'analyse doit etre qualifiee")
	}
}

func TestPuissanceMontanteAuPlafondEstCritique(t *testing.T) {
	// Chez VOO, les canaux tombent au-dela de 52 dBmV. C'est le defaut le
	// plus frequent sur une voie retour degradee.
	h := Snapshot(releve(nil,
		[]docsis.RawUpstream{{ChannelID: 5, Modulation: "64QAM", PowerLevel: "53.0"}},
	), profilVOO(t))

	if h.Level != Critical {
		t.Errorf("verdict %s, attendu critique", h.Level)
	}
}

func TestPuissanceMontanteNormaleSous49(t *testing.T) {
	h := Snapshot(releve(nil,
		[]docsis.RawUpstream{{ChannelID: 5, Modulation: "64QAM", PowerLevel: "48.0"}},
	), profilVOO(t))
	if h.Level != Good {
		t.Errorf("48 dBmV devrait etre bon, obtenu %s", h.Level)
	}
}

func TestCanalOFDMJugeSurSesPropresSeuils(t *testing.T) {
	// Sans ligne ofdm dediee, 27 dB seraient juges sur le plancher de 40 dB
	// du 4096QAM et ressortiraient en critique alors que le canal va bien.
	h := Snapshot(releve(
		[]docsis.RawDownstream{{ChannelID: 159, Modulation: "OFDM",
			PowerLevel: "7.5", MSE: "-27.5"}}, nil), profilVOO(t))

	if h.Level != Good {
		t.Errorf("verdict %s, attendu bon : %+v", h.Level, h.Downstream[0].Findings)
	}
}

func TestSNRDegradeEnQAMEleve(t *testing.T) {
	h := Snapshot(releve(
		[]docsis.RawDownstream{{ChannelID: 1, Modulation: "4096QAM",
			PowerLevel: "3.0", MSE: "-34.0"}}, nil), profilVOO(t))
	if h.Level != Critical {
		t.Errorf("34 dB en 4096QAM devrait etre critique, obtenu %s", h.Level)
	}
}

func TestValeurAbsenteResteInconnueEtNePenalisePas(t *testing.T) {
	// Un modem en cours de synchronisation ne doit pas etre rapporte en panne.
	h := Snapshot(releve(
		[]docsis.RawDownstream{{ChannelID: 1, Modulation: "256QAM",
			PowerLevel: "", MSE: ""}}, nil), profilVOO(t))

	if h.Level != Unknown {
		t.Errorf("verdict %s, attendu inconnu", h.Level)
	}
	if len(h.Downstream[0].Findings) != 0 {
		t.Errorf("aucun constat attendu, obtenu %+v", h.Downstream[0].Findings)
	}
}

func TestModulationMontanteBasseAvertitSansConclure(t *testing.T) {
	h := Snapshot(releve(nil,
		[]docsis.RawUpstream{{ChannelID: 5, Modulation: "16QAM", PowerLevel: "45.0"}},
	), profilVOO(t))

	if h.Level != Warning {
		t.Errorf("16QAM devrait avertir, obtenu %s", h.Level)
	}
	var trouve bool
	for _, f := range h.Upstream[0].Findings {
		if f.Metric == "modulation montante" {
			trouve = true
			// Le message doit inviter a comparer l'historique : un segment
			// configure en 16QAM depuis l'installation n'est pas degrade.
			if f.Message == "16QAM" {
				t.Error("le message devrait nuancer le verdict")
			}
		}
	}
	if !trouve {
		t.Error("aucun constat sur la modulation")
	}
}

func TestLesErreursSeJugentSurLaFenetre(t *testing.T) {
	canal := releve([]docsis.RawDownstream{{ChannelID: 1, Modulation: "256QAM",
		PowerLevel: "3.0", MSE: "-40.0", CorrErrors: 5_000_000, NonCorrErrors: 900_000}}, nil)
	p := profilVOO(t)
	niveau := func(e *ErrorCount) (Level, string) {
		opts := Options{}
		if e != nil {
			opts.Errors = map[int]ErrorCount{1: *e}
		}
		h := SnapshotWith(canal, p, opts)
		for _, f := range h.Downstream[0].Findings {
			if f.Metric == "erreurs" {
				return h.Level, f.Message
			}
		}
		return h.Level, ""
	}

	// Les compteurs bruts cumulent depuis le demarrage du modem : sans
	// fenetre, ils ne sont pas juges, si enormes soient-ils.
	if l, msg := niveau(nil); l != Good || msg != "" {
		t.Errorf("compteurs bruts juges : %s %q", l, msg)
	}
	cas := []struct {
		e    ErrorCount
		want Level
	}{
		{ErrorCount{}, Good},
		{ErrorCount{Corrected: 2, Uncorrected: 1}, Good},           // trop peu pour une proportion
		{ErrorCount{Corrected: 999_000, Uncorrected: 1_000}, Good}, // 0,1 %
		// Cas reel : 30 pertes sur 2 151 erreurs, 1,4 %, mais imperceptible.
		{ErrorCount{Corrected: 2_121, Uncorrected: 30}, Good},
		{ErrorCount{Corrected: 9_800, Uncorrected: 200}, Good},       // 2 %, mais 200 pertes
		{ErrorCount{Corrected: 98_000, Uncorrected: 2_000}, Warning}, // 2 % et 2 000 pertes
		{ErrorCount{Corrected: 9_000, Uncorrected: 1_000}, Critical}, // 10 % et 1 000 pertes
	}
	for _, c := range cas {
		if l, msg := niveau(&c.e); l != c.want || !strings.Contains(msg, "24") {
			t.Errorf("%+v : %s (%q), attendu %s", c.e, l, msg, c.want)
		}
	}
}

func TestProfilNonCalibreNeQualifieRien(t *testing.T) {
	p, err := operator.Load("generique")
	if err != nil {
		t.Fatal(err)
	}
	h := Snapshot(releve(
		[]docsis.RawDownstream{{ChannelID: 1, Modulation: "256QAM",
			PowerLevel: "99.0", MSE: "-5.0"}}, nil), p)

	if h.Qualified {
		t.Error("un profil non calibre ne doit pas se declarer qualifie")
	}
	if h.Level != Unknown || len(h.Downstream) != 0 {
		t.Errorf("aucun verdict attendu, obtenu %s avec %d canaux", h.Level, len(h.Downstream))
	}
}

func TestLePireCanalDetermineLeVerdictGlobal(t *testing.T) {
	h := Snapshot(releve([]docsis.RawDownstream{
		{ChannelID: 1, Modulation: "256QAM", PowerLevel: "3.0", MSE: "-40.0"},
		{ChannelID: 2, Modulation: "256QAM", PowerLevel: "20.0", MSE: "-40.0"},
	}, nil), profilVOO(t))

	if h.Level != Critical {
		t.Errorf("verdict global %s, un canal hors limites devrait le rendre critique", h.Level)
	}
	if h.Downstream[0].Level != Good {
		t.Error("le canal sain ne doit pas etre degrade par son voisin")
	}
}

func TestOFDMAMontantBasResteBon(t *testing.T) {
	// Releve reel : 30,2 dBmV sur l'OFDMA montant. Avec une borne basse a 30,
	// deux dixiemes de moins auraient suffi a declencher un avertissement sur
	// une ligne saine.
	for _, p := range []string{"30.2", "29.0", "28.5"} {
		h := Snapshot(releve(nil,
			[]docsis.RawUpstream{{ChannelID: 5, Modulation: "OFDMA", PowerLevel: p}},
		), profilVOO(t))
		if h.Level != Good {
			t.Errorf("%s dBmV: verdict %s, attendu bon", p, h.Level)
		}
	}
}

func TestOFDMABasQAMSignalePaDefaut(t *testing.T) {
	// Sans declaration, un OFDMA a 16QAM est une degradation.
	h := Snapshot(releve(nil, []docsis.RawUpstream{
		{ChannelID: 5, Modulation: "OFDMA", Multiplex: "16QAM", PowerLevel: "45.0"},
	}), profilVOO(t))
	if h.Level != Critical {
		t.Errorf("16QAM OFDMA sans declaration: %s, attendu critique", h.Level)
	}
}

func TestModulationHabituelleDeclaree(t *testing.T) {
	// Segment declare a 16QAM : c'est la reference, et les variations
	// restent jugees par rapport a elle.
	opts := Options{OFDMAFloor: 16}
	cas := map[string]Level{"64QAM": Good, "32QAM": Good, "16QAM": Good, "8QAM": Warning, "4QAM": Critical}
	for m, want := range cas {
		h := SnapshotWith(releve(nil, []docsis.RawUpstream{
			{ChannelID: 5, Modulation: "OFDMA", Multiplex: m, PowerLevel: "45.0"},
		}), profilVOO(t), opts)
		if h.Level != want {
			t.Errorf("%s, habituel 16QAM : %s, attendu %s", m, h.Level, want)
		}
		if m == "16QAM" && !strings.Contains(h.Upstream[0].Findings[len(h.Upstream[0].Findings)-1].Message, "habituelle") {
			t.Errorf("le constat ne dit pas que 16QAM est l'habitude du segment : %+v", h.Upstream[0].Findings)
		}
	}
}

func TestLaDeclarationNeTouchePasLeSCQAM(t *testing.T) {
	// L'interrupteur ne concerne que l'OFDMA : un SC-QAM a 16QAM reste un
	// avertissement.
	h := SnapshotWith(releve(nil, []docsis.RawUpstream{
		{ChannelID: 1, Modulation: "16QAM", PowerLevel: "45.0"},
	}), profilVOO(t), Options{OFDMAFloor: 16})
	if h.Level != Warning {
		t.Errorf("SC-QAM 16QAM: %s, attendu avertissement", h.Level)
	}
}
