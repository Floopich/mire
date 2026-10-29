package webview

import (
	"net/http"
	"strings"
	"testing"
)

func TestPageComprendre(t *testing.T) {
	h, cookie := serveurAvec(t, 3, essai{Collecting: true})
	if rec := get(t, h, "", "/comprendre"); rec.Code != http.StatusSeeOther {
		t.Errorf("sans session : HTTP %d, attendu une redirection", rec.Code)
	}
	rec := get(t, h, cookie, "/comprendre")
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d", rec.Code)
	}
	body := rec.Body.String()
	for _, voulu := range []string{
		// Seuils du profil VOO, tels que l'analyse les applique.
		"la plage normale va de −8 à &#43;8\u00a0dBmV ; jusqu&#39;à ±10\u00a0dBmV, c&#39;est à surveiller ; au-delà, critique.",
		"35 dB et plus est bon, sous 35 dB à surveiller, sous 33 dB critique.",
		"un canal est à surveiller à partir de 1\u202f000 erreurs non corrigées sur la période et 1 % de ses erreurs, critique à partir de 3 %.",
		// Valeurs du dernier releve de la base de test.
		"Chez vous : 2 canaux descendants, 1 canal montant.",
		"Chez vous : de 1,5\u00a0dBmV à 2,1\u00a0dBmV.",
		"Chez vous : 1 SC-QAM et 1 OFDM en voie descendante, 1 SC-QAM en voie montante.",
		`href="#agir"`,
	} {
		if !strings.Contains(body, voulu) {
			t.Errorf("absent : %s", voulu)
		}
	}
	// La vue d'ensemble y mene.
	if !strings.Contains(get(t, h, cookie, "/").Body.String(), `href="/comprendre"`) {
		t.Error("aucun lien depuis la vue d'ensemble")
	}
}
