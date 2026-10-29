package operator

import "testing"

func TestVooPorteLesSeuilsDeTerrain(t *testing.T) {
	p, err := Load("voo")
	if err != nil {
		t.Fatalf("chargement: %v", err)
	}
	if !p.Calibrated {
		t.Error("le profil VOO doit etre marque calibre")
	}
	if p.DefaultModemURL != "http://192.168.100.1" {
		t.Errorf("adresse par defaut: %q", p.DefaultModemURL)
	}

	ds, ok := p.Thresholds.DownstreamPowerFor("256QAM")
	if !ok || ds.Good.Min != -8 || ds.Good.Max != 8 {
		t.Errorf("plage descendante bonne: %+v", ds.Good)
	}
	// La borne haute montante est le point sensible chez VOO : au-dela de 52
	// les canaux tombent, et c'est ce que le profil doit refuser de tolerer.
	us, ok := p.Thresholds.UpstreamPowerFor("sc_qam")
	if !ok || us.Good.Max != 49 || us.Critical.Max != 52 {
		t.Errorf("plage montante: bonne %+v, critique %+v", us.Good, us.Critical)
	}
	// Sans ligne ofdm dediee, un canal OFDM serait juge sur le plancher du
	// 4096QAM alors que son MER agrege se mesure autrement.
	ofdm, ok := p.Thresholds.SNRFor("ofdm")
	if !ok || ofdm.GoodMin != 27 || ofdm.WarningMin != 25.5 {
		t.Errorf("SNR ofdm: %+v", ofdm)
	}
	if qam, _ := p.Thresholds.SNRFor("4096QAM"); qam.GoodMin != 40 {
		t.Errorf("SNR 4096QAM: %+v", qam)
	}
}

func TestProfilGeneriqueNeQualifiePas(t *testing.T) {
	p, err := Load("generique")
	if err != nil {
		t.Fatalf("chargement: %v", err)
	}
	if p.Calibrated {
		t.Error("le profil generique ne doit pas se declarer calibre")
	}
	if _, ok := p.Thresholds.DownstreamPowerFor("256QAM"); ok {
		t.Error("un profil non calibre ne doit renvoyer aucun seuil")
	}
}

func TestModulationInconnueRetombeSurLeDefaut(t *testing.T) {
	p, _ := Load("voo")
	got, ok := p.Thresholds.SNRFor("2048QAM")
	want, _ := p.Thresholds.SNRFor("256QAM")
	if !ok || got != want {
		t.Errorf("modulation inconnue: attendu le defaut %+v, obtenu %+v", want, got)
	}
}

func TestVariantesDEcritureDeLaModulation(t *testing.T) {
	p, _ := Load("voo")
	ref, _ := p.Thresholds.SNRFor("1024QAM")
	for _, written := range []string{"1024 QAM", "qam1024", "1024-qam", "1024qam"} {
		got, ok := p.Thresholds.SNRFor(written)
		if !ok || got != ref {
			t.Errorf("%q: attendu %+v, obtenu %+v", written, ref, got)
		}
	}
}

func TestTousLesProfilsSeChargent(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatalf("lecture: %v", err)
	}
	if len(all) < 2 {
		t.Fatalf("%d profils trouves", len(all))
	}
	for _, p := range all {
		if p.Name == "" || p.DefaultModemURL == "" {
			t.Errorf("%s: nom ou adresse manquants", p.ID)
		}
		if !p.Calibrated && p.Notes == "" {
			t.Errorf("%s: un profil non calibre doit dire pourquoi", p.ID)
		}
	}
}

func TestProfilInconnuEchoue(t *testing.T) {
	if _, err := Load("inexistant"); err == nil {
		t.Error("un profil absent doit renvoyer une erreur")
	}
}

func TestTelenetEstCalibreSurLaSpecificationDOCSIS(t *testing.T) {
	p, err := Load("telenet")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Calibrated || p.Source == "" || p.Country != "BE" || p.DefaultModem != "" {
		t.Errorf("profil %+v", p)
	}
	ds, _ := p.Thresholds.DownstreamPowerFor("256QAM")
	if ds.Critical.Min != -15 || ds.Critical.Max != 15 {
		t.Errorf("plage d'entrée DOCSIS : %+v", ds.Critical)
	}
	snr, _ := p.Thresholds.SNRFor("256QAM")
	if snr.WarningMin != 31 {
		t.Errorf("minimum 256QAM : %+v", snr)
	}
	us, _ := p.Thresholds.UpstreamPowerFor("sc_qam")
	if us.Warning.Max != 50 || us.Critical.Max != 52 {
		t.Errorf("émission : %+v", us)
	}
}
