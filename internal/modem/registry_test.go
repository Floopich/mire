package modem

import (
	"regexp"
	"strings"
	"testing"
)

func TestLeRegistreEstCoherent(t *testing.T) {
	id := regexp.MustCompile(`^[a-z0-9-]+$`)
	vus := map[string]bool{}
	for _, m := range Models() {
		if !id.MatchString(m.ID) {
			t.Errorf("identifiant %q : minuscules, chiffres et tirets seulement", m.ID)
		}
		if vus[m.ID] {
			t.Errorf("identifiant %q en double", m.ID)
		}
		vus[m.ID] = true
		if m.Name == "" || m.New == nil {
			t.Errorf("%s : nom ou constructeur manquant", m.ID)
		}
		if len(m.Markers) == 0 {
			t.Errorf("%s : aucun repere pour la detection", m.ID)
		}
		for _, mk := range m.Markers {
			if mk != strings.ToLower(mk) || mk == "docsis" {
				t.Errorf("%s : repere %q, attendu en minuscules et propre au modele", m.ID, mk)
			}
		}
		d, err := m.New(Config{URL: "http://192.168.100.1", User: m.DefaultUser, Password: "x"})
		if err != nil || d == nil {
			t.Errorf("%s : construction impossible : %v", m.ID, err)
		}
	}
}

func TestModeleInconnuListeLesModelesConnus(t *testing.T) {
	_, err := Lookup("inexistant")
	if err == nil || !regexp.MustCompile(`cga4233`).MatchString(err.Error()) {
		t.Errorf("erreur attendue avec la liste des modeles, obtenu %v", err)
	}
	if m, err := Lookup(" CGA4233 "); err != nil || m.ID != "cga4233" {
		t.Errorf("la recherche doit ignorer la casse et les espaces : %v", err)
	}
}
