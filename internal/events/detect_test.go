package events

import (
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/operator"
	"github.com/floopich/mire/internal/store"
)

var t0 = time.Unix(1_700_000_000, 0).UTC()

func voo(t *testing.T) operator.Profile {
	t.Helper()
	p, err := operator.Load("voo")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// ligne fabrique un releve : canaux descendants 1 a n aux compteurs donnes,
// canaux montants aux modulations donnees.
func ligne(at time.Time, n int, corr, noncorr int64, up ...string) docsis.Snapshot {
	s := docsis.Snapshot{Timestamp: at}
	for id := 1; id <= n; id++ {
		s.Downstream = append(s.Downstream, docsis.Downstream{ChannelID: id, Modulation: "256QAM",
			CorrErrors: corr, NonCorrErrors: noncorr})
	}
	for i, m := range up {
		s.Upstream = append(s.Upstream, docsis.Upstream{ChannelID: i + 1, Modulation: m})
	}
	return s
}

func kinds(evs []store.Event) string {
	var k []string
	for _, e := range evs {
		k = append(k, e.Kind)
	}
	return strings.Join(k, ",")
}

func TestUnReleveIdentiqueNeProduitRien(t *testing.T) {
	prev := ligne(t0, 8, 100, 1, "64QAM")
	cur := ligne(t0.Add(10*time.Minute), 8, 120, 1, "64QAM")
	evs := Detect(Input{Prev: &prev, Cur: cur, Profile: voo(t),
		Health: analyze.Health{Qualified: true, Level: analyze.Tolerated}})
	if len(evs) != 0 {
		t.Errorf("evenements inattendus : %s", kinds(evs))
	}
}

func TestRedemarrageDuModem(t *testing.T) {
	prev := ligne(t0, 8, 50_000, 300)
	cur := ligne(t0.Add(10*time.Minute), 8, 12, 0)
	evs := Detect(Input{Prev: &prev, Cur: cur, Profile: voo(t)})
	if kinds(evs) != KindRestart {
		t.Fatalf("evenements %s, attendu un redemarrage", kinds(evs))
	}

	// Un seul canal remis a zero n'est pas un redemarrage.
	cur = ligne(t0.Add(10*time.Minute), 8, 50_100, 300)
	cur.Downstream[0].CorrErrors, cur.Downstream[0].NonCorrErrors = 0, 0
	if evs := Detect(Input{Prev: &prev, Cur: cur, Profile: voo(t)}); len(evs) != 0 {
		t.Errorf("un canal isole remis a zero : %s", kinds(evs))
	}
}

func TestPicDErreurs(t *testing.T) {
	prev := ligne(t0, 4, 0, 1_000)
	cur := ligne(t0.Add(10*time.Minute), 4, 0, 1_300) // 4 x 300 = 1 200 non corrigees
	evs := Detect(Input{Prev: &prev, Cur: cur, Profile: voo(t)})
	if kinds(evs) != KindErrors || !strings.Contains(evs[0].Message, "1 200") {
		t.Fatalf("evenements %s %+v, attendu un pic de 1 200", kinds(evs), evs)
	}
	// Le pic continue : rien de plus. Il cesse : sa fin donne le total, sur
	// les deux intervalles touches (de t0 a t0+20 min).
	debut := evs[0]
	suite := ligne(t0.Add(20*time.Minute), 4, 0, 1_600)
	if evs := Detect(Input{Prev: &cur, Cur: suite, Profile: voo(t), OpenSpike: &debut}); len(evs) != 0 {
		t.Errorf("pic en cours inscrit de nouveau : %s", kinds(evs))
	}
	calme := ligne(t0.Add(30*time.Minute), 4, 0, 1_601)
	evs = Detect(Input{Prev: &suite, Cur: calme, Profile: voo(t), OpenSpike: &debut, SpikeTotal: 2_404})
	if kinds(evs) != KindErrorsEnd || evs[0].Message != "Fin du pic d'erreurs : 2\u202f404 non corrigées en 20\u00a0min." {
		t.Errorf("fin du pic : %+v", evs)
	}

	// Sans seuils calibres, aucun pic n'est juge.
	generique, _ := operator.Load("generique")
	if evs := Detect(Input{Prev: &prev, Cur: cur, Profile: generique}); len(evs) != 0 {
		t.Errorf("profil non calibre : %s", kinds(evs))
	}
}

func TestCanauxPerdusEtRetrouves(t *testing.T) {
	prev := ligne(t0, 5, 0, 0, "64QAM", "64QAM")
	cur := ligne(t0.Add(10*time.Minute), 3, 0, 0, "64QAM")
	evs := Detect(Input{Prev: &prev, Cur: cur, Profile: voo(t)})
	if kinds(evs) != KindChannelsLost+","+KindChannelsLost {
		t.Fatalf("evenements %s", kinds(evs))
	}
	if evs[0].Message != "Canaux descendants perdus : 4 et 5." || evs[0].ChannelID != 0 {
		t.Errorf("message %q", evs[0].Message)
	}
	if evs[1].Message != "Canal montant 2 perdu." || evs[1].ChannelID != 2 || evs[1].Direction != "montant" {
		t.Errorf("message %q, canal %d", evs[1].Message, evs[1].ChannelID)
	}

	back := Detect(Input{Prev: &cur, Cur: prev, Profile: voo(t)})
	if kinds(back) != KindChannelsBack+","+KindChannelsBack || back[0].Level != int(analyze.Good) {
		t.Errorf("retour : %s", kinds(back))
	}
}

func TestModulation(t *testing.T) {
	prev := ligne(t0, 2, 0, 0, "64QAM", "64QAM", "OFDMA")
	prev.Upstream[2].Multiplex = "256QAM"
	cur := ligne(t0.Add(10*time.Minute), 2, 0, 0, "16QAM", "64QAM", "OFDMA")
	cur.Upstream[2].Multiplex = "64QAM"
	evs := Detect(Input{Prev: &prev, Cur: cur, Profile: voo(t)})
	if kinds(evs) != KindModulation || evs[0].Level != int(analyze.Warning) {
		t.Fatalf("evenements %s", kinds(evs))
	}
	want := "Modulation en baisse, voie montante : canal 1 (64QAM → 16QAM) et canal 3 (256QAM → 64QAM)."
	if evs[0].Message != want {
		t.Errorf("message %q", evs[0].Message)
	}
}

func TestChangementsDEtat(t *testing.T) {
	p := voo(t)
	critique := analyze.Health{Qualified: true, Level: analyze.Critical, Downstream: []analyze.ChannelHealth{
		{ChannelID: 22, Level: analyze.Critical, Findings: []analyze.Finding{
			{Level: analyze.Good, Metric: "SNR", Message: "40,0 dB"},
			{Level: analyze.Critical, Metric: "puissance descendante", Message: "16,0 dBmV, hors limites"},
		}},
	}}
	evs := Detect(Input{Cur: ligne(t0, 1, 0, 0), Health: critique, Profile: p})
	if len(evs) != 1 || evs[0].Kind != KindState || evs[0].ChannelID != 22 {
		t.Fatalf("passage en critique : %+v", evs)
	}
	if want := "Ligne critique : canal descendant 22, puissance descendante 16,0 dBmV, hors limites."; evs[0].Message != want {
		t.Errorf("message %q", evs[0].Message)
	}

	// Meme etat que le dernier inscrit : rien.
	last := evs[0]
	if evs := Detect(Input{Cur: ligne(t0, 1, 0, 0), Health: critique, LastState: &last, Profile: p}); len(evs) != 0 {
		t.Errorf("etat inchange inscrit de nouveau : %+v", evs)
	}

	// Un canal tolere n'est pas une alerte : retour au bon etat.
	tolere := analyze.Health{Qualified: true, Level: analyze.Tolerated}
	evs = Detect(Input{Cur: ligne(t0, 1, 0, 0), Health: tolere, LastState: &last, Profile: p})
	if len(evs) != 1 || evs[0].Message != "Ligne revenue en bon état." {
		t.Errorf("retour au bon etat : %+v", evs)
	}

	// Premier releve en bon etat, ou profil non calibre : rien a inscrire.
	if evs := Detect(Input{Cur: ligne(t0, 1, 0, 0), Health: tolere, Profile: p}); len(evs) != 0 {
		t.Errorf("premier releve sain : %+v", evs)
	}
	if evs := Detect(Input{Cur: ligne(t0, 1, 0, 0), Health: analyze.Health{}, Profile: p}); len(evs) != 0 {
		t.Errorf("profil non calibre : %+v", evs)
	}
}

func TestPlusieursCanauxEnCause(t *testing.T) {
	h := analyze.Health{Qualified: true, Level: analyze.Warning}
	for _, id := range []int{3, 5, 9} {
		h.Downstream = append(h.Downstream, analyze.ChannelHealth{ChannelID: id, Level: analyze.Warning,
			Findings: []analyze.Finding{{Level: analyze.Warning, Metric: "SNR", Message: "33,0 dB, sous le seuil nominal"}}})
	}
	evs := Detect(Input{Cur: ligne(t0, 1, 0, 0), Health: h, Profile: voo(t)})
	if len(evs) != 1 || evs[0].ChannelID != 0 || !strings.HasSuffix(evs[0].Message, "et 2 autres canaux.") {
		t.Errorf("%+v", evs)
	}
}

func TestDurees(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * time.Second:               "moins d'une minute",
		35 * time.Minute:               "35 min",
		2 * time.Hour:                  "2 h",
		2*time.Hour + 5*time.Minute:    "2 h 05",
		3*24*time.Hour + 4*time.Minute: "3 j",
	} {
		if got := Duration(d); got != want {
			t.Errorf("%v : %q, attendu %q", d, got, want)
		}
	}
}
