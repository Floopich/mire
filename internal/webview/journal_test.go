package webview

import (
	"bytes"
	"strings"
	"testing"

	"github.com/floopich/mire/internal/journal"
)

func TestLaRubriqueJournalAfficheLesDernieresLignes(t *testing.T) {
	jr, log := journal.New(&bytes.Buffer{})
	log.Info("collecte démarrée", "modele", "cga4233")
	log.Warn("redémarrage du modem refusé", "erreur", "le modem dit non")
	h, token := serveurAvec(t, 0, essai{Journal: jr})

	body := get(t, h, token, "/reglages/journal").Body.String()
	for _, voulu := range []string{"Niveau de détail", "Alertes et erreurs", "collecte démarrée", "modele=cga4233",
		`class="surveiller"`, "redémarrage du modem refusé", "/reglages/journal/telecharger"} {
		if !strings.Contains(body, voulu) {
			t.Errorf("absent : %s", voulu)
		}
	}
	if strings.Index(body, "redémarrage du modem refusé") > strings.Index(body, "collecte démarrée") {
		t.Error("la ligne la plus récente doit venir en premier")
	}
	if !strings.Contains(get(t, h, token, "/reglages").Body.String(), "Niveau normal") {
		t.Error("résumé de la rubrique absent")
	}

	rec := get(t, h, token, "/reglages/journal/telecharger")
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") ||
		!strings.Contains(rec.Body.String(), "INFO collecte démarrée modele=cga4233\n") ||
		strings.Index(rec.Body.String(), "WARN") < strings.Index(rec.Body.String(), "INFO") {
		t.Errorf("téléchargement : %q", rec.Body.String())
	}
}

func TestLAssistantProposeLesOptions(t *testing.T) {
	h, token := serveurAvec(t, 0, essai{})
	body := get(t, h, token, "/installation/options").Body.String()
	for _, voulu := range []string{"Options", "/reglages/domotique", "/reglages/notifications", "/reglages/debit", "/reglages/metriques", `href="/">Terminer`} {
		if !strings.Contains(body, voulu) {
			t.Errorf("absent : %s", voulu)
		}
	}
}
