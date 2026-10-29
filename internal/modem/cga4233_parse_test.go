package modem

import (
	"os"
	"testing"
	"time"

	"github.com/floopich/mire/internal/docsis"
)

// reponse reproduit la forme du firmware CGA4233VOO : deux canaux SC-QAM
// descendants, un OFDM, un montant SC-QAM, un OFDMA. ErrTbl compte trois
// lignes : deux pour DSTbl, la troisieme pour le canal OFDM.
const reponse = `{"data":{
 "DSTbl":[
  {"ChannelID":"1","Modulation":"256qam","Frequency":"650000000","PowerLevel":"1.5","SNRLevel":"38.9","Correcteds":"120","Uncorrectables":"3"},
  {"ChannelID":2,"Modulation":"256QAM","Frequency":658000000,"PowerLevel":1.7,"SNRLevel":38.5,"Correcteds":100,"Uncorrectables":0}
 ],
 "exDSTbl":[
  {"ChannelID":"33","CentralFrequency":"800000000","PowerLevel":"2.1","SNRLevel":"40.2","FFT":"1024-qam"}
 ],
 "USTbl":[
  {"ChannelID":"5","Modulation":"64qam","Frequency":"42000000","PowerLevel":"45.5"}
 ],
 "exUSTbl":[
  {"ChannelID":"9","CentralFrequency":"60000000","PowerLevel":"46.0","FFT":"OFDMA"}
 ],
 "ErrTbl":[
  {"Correcteds":"120","Uncorrectables":"3"},
  {"Correcteds":"100","Uncorrectables":"0"},
  {"Correcteds":"7","Uncorrectables":"2"}
 ]}}`

func TestParsePayloadLitLesQuatreTables(t *testing.T) {
	snap, err := parsePayload([]byte(reponse))
	if err != nil {
		t.Fatalf("analyse: %v", err)
	}
	if len(snap.Downstream) != 3 {
		t.Fatalf("%d canaux descendants, attendu 3", len(snap.Downstream))
	}
	if len(snap.Upstream) != 2 {
		t.Fatalf("%d canaux montants, attendu 2", len(snap.Upstream))
	}
}

func TestCompteursOFDMViennentDeLaBonneLigneDErrTbl(t *testing.T) {
	// Le piege : ErrTbl est positionnel. Si l'OFDM prenait la premiere ligne
	// au lieu de la troisieme, il afficherait 120/3 au lieu de 7/2 -- une
	// erreur invisible a l'oeil sur un tableau de quarante canaux.
	snap, err := parsePayload([]byte(reponse))
	if err != nil {
		t.Fatalf("analyse: %v", err)
	}
	ofdm := snap.Downstream[2]
	if ofdm.ChannelID != 33 || ofdm.Modulation != "OFDM" {
		t.Fatalf("troisieme canal inattendu: %+v", ofdm)
	}
	if ofdm.CorrErrors != 7 || ofdm.NonCorrErrors != 2 {
		t.Errorf("compteurs OFDM %d/%d, attendu 7/2", ofdm.CorrErrors, ofdm.NonCorrErrors)
	}
}

func TestSNRPositifDevientMSENegativePuisSNRPositif(t *testing.T) {
	snap, _ := parsePayload([]byte(reponse))
	if snap.Downstream[0].MSE != "-38.9" {
		t.Errorf("MSE brute %q, attendu -38.9", snap.Downstream[0].MSE)
	}
	got := snap.Normalize(time.Now()).Downstream[0].SNRDB
	if got == nil || *got != 38.9 {
		t.Errorf("SNR normalise %v, attendu 38.9", got)
	}
}

func TestFrequenceEnMHz(t *testing.T) {
	// Les deux unites existent selon le firmware : Hz ici, MHz chez VOO.
	snap, _ := parsePayload([]byte(reponse))
	if got := snap.Downstream[0].Frequency; got != "650 MHz" {
		t.Errorf("frequence en Hz mal convertie : %q", got)
	}
	for v, voulu := range map[float64]string{650: "650 MHz", 37.2: "37.2 MHz", 706.487488: "706.5 MHz", 50.66: "50.7 MHz"} {
		if got := mhz(docsis.FlexFloat(v)); got != voulu {
			t.Errorf("mhz(%v) = %q, attendu %q", v, got, voulu)
		}
	}
	f := snap.Normalize(time.Now()).Downstream[0].FrequencyMHz
	if f == nil || *f != 650 {
		t.Fatalf("frequence normalisee %v, attendu 650", f)
	}
}

func TestModulationNormalisee(t *testing.T) {
	snap, _ := parsePayload([]byte(reponse))
	if snap.Downstream[0].Modulation != "256QAM" {
		t.Errorf("%q, attendu 256QAM", snap.Downstream[0].Modulation)
	}
	if snap.Upstream[0].Modulation != "64QAM" {
		t.Errorf("%q, attendu 64QAM", snap.Upstream[0].Modulation)
	}
	if snap.Upstream[1].Modulation != "OFDMA" {
		t.Errorf("%q, attendu OFDMA", snap.Upstream[1].Modulation)
	}
}

func TestTablesAbsentesNeFontPasEchouer(t *testing.T) {
	// Un modem en cours de synchronisation renvoie des tables vides : c'est un
	// releve pauvre, pas une erreur.
	snap, err := parsePayload([]byte(`{"data":{}}`))
	if err != nil {
		t.Fatalf("analyse: %v", err)
	}
	if len(snap.Downstream) != 0 || len(snap.Upstream) != 0 {
		t.Error("tables vides devraient donner un releve vide")
	}
}

func TestTablesALaRacineSontAcceptees(t *testing.T) {
	snap, err := parsePayload([]byte(`{"DSTbl":[{"ChannelID":"1","Frequency":"442 MHz","PowerLevel":"1.0"}]}`))
	if err != nil {
		t.Fatalf("analyse: %v", err)
	}
	if len(snap.Downstream) != 1 {
		t.Errorf("%d canaux, attendu 1", len(snap.Downstream))
	}
}

func TestReponseIllisibleEstUneErreur(t *testing.T) {
	if _, err := parsePayload([]byte("<html>401</html>")); err == nil {
		t.Error("une reponse non JSON doit remonter une erreur")
	}
}

func TestEmplacementsVidesIgnores(t *testing.T) {
	// Le modem publie une ligne par recepteur, meme sans canal accroche :
	// identifiant nul ou aberrant, sans frequence, ou decroche. Rien de cela
	// n'est un canal. La ligne OFDM vide compte pourtant dans la position des
	// compteurs d'ErrTbl : l'OFDM qui la suit garde les siens.
	const vide = `{"data":{
	 "DSTbl":[{"ChannelID":"1","Modulation":"256qam","Frequency":"442","PowerLevel":"2.1","SNRLevel":"40.4"}],
	 "exDSTbl":[
	  {"ChannelID":"0","CentralFrequency":"0","PowerLevel":"0","SNRLevel":"0","FFT":""},
	  {"ChannelID":"159","CentralFrequency":"750","PowerLevel":"6.5","SNRLevel":"41","FFT":"4K"}
	 ],
	 "USTbl":[
	  {"ChannelID":"0","Modulation":"","Frequency":"0","PowerLevel":"0"},
	  {"ChannelID":"1293955120","Modulation":"","Frequency":"","PowerLevel":""},
	  {"ChannelID":"3","Modulation":"32-qam","Frequency":"50.7 MHz","PowerLevel":"35.5 dBmV","LockStatus":"Not Locked"},
	  {"ChannelID":"7","Modulation":"32-qam","Frequency":"","PowerLevel":"35.5 dBmV"}
	 ],
	 "ErrTbl":[
	  {"Correcteds":"1","Uncorrectables":"0"},
	  {"Correcteds":"0","Uncorrectables":"0"},
	  {"Correcteds":"9","Uncorrectables":"4"}
	 ]}}`
	snap, err := parsePayload([]byte(vide))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Downstream) != 2 || len(snap.Upstream) != 0 {
		t.Fatalf("%d descendants, %d montants : attendu 2 et 0", len(snap.Downstream), len(snap.Upstream))
	}
	ofdm := snap.Downstream[1]
	if ofdm.ChannelID != 159 || ofdm.CorrErrors != 9 || ofdm.NonCorrErrors != 4 {
		t.Errorf("canal OFDM inattendu : %+v", ofdm)
	}
}

// TestReponseReelleFirmwareVOO rejoue une reponse du firmware VOO d'octobre
// 2026. exDSTbl, exUSTbl et la ligne OFDM d'ErrTbl sont reprises telles
// quelles, avec un second emplacement OFDMA vide releve le meme jour sous
// l'identifiant aberrant 1293955120 ; DSTbl, USTbl et les autres lignes d'ErrTbl gardent les valeurs et
// la forme relevees (unites dans les valeurs, identifiants en texte).
func TestReponseReelleFirmwareVOO(t *testing.T) {
	raw, err := os.ReadFile("testdata/cga4233-voo.json")
	if err != nil {
		t.Fatal(err)
	}
	snap, err := parsePayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	// 16 SC-QAM et l'OFDM 159 ; 4 SC-QAM montants, les emplacements OFDMA
	// vides ne sont pas des canaux.
	if len(snap.Downstream) != 17 || len(snap.Upstream) != 4 {
		t.Fatalf("%d descendants, %d montants : attendu 17 et 4", len(snap.Downstream), len(snap.Upstream))
	}
	n := snap.Normalize(time.Now())
	ofdm := n.Downstream[16]
	if ofdm.ChannelID != 159 || ofdm.Modulation != "OFDM" || *ofdm.FrequencyMHz != 706.5 ||
		*ofdm.PowerDBmV != 26.6 || *ofdm.SNRDB != 43.5 {
		t.Errorf("OFDM 159 mal lu : %+v", snap.Downstream[16])
	}
	if ofdm.CorrErrors != 3922065920 || ofdm.NonCorrErrors != 17195 {
		t.Errorf("compteurs OFDM %d/%d, attendu ceux de la 17e ligne d'ErrTbl", ofdm.CorrErrors, ofdm.NonCorrErrors)
	}
	sc := n.Downstream[0]
	if sc.ChannelID != 1 || sc.Modulation != "256QAM" || *sc.PowerDBmV != 2.1 || *sc.SNRDB != 40.4 {
		t.Errorf("SC-QAM 1 mal lu : %+v", snap.Downstream[0])
	}
	us := n.Upstream[2]
	if us.ChannelID != 3 || us.Modulation != "32QAM" || *us.FrequencyMHz != 50.7 || *us.PowerDBmV != 35.5 {
		t.Errorf("montant 3 mal lu : %+v", snap.Upstream[2])
	}
}
