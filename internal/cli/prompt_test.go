package cli

import (
	"os"
	"strings"
	"testing"
)

func TestMotDePasseLuSansLeSautDeLigne(t *testing.T) {
	var sortie strings.Builder
	got, err := readPasswordFrom(strings.NewReader("secret123\n"), &sortie, "invite : ", false)
	if err != nil {
		t.Fatalf("lecture: %v", err)
	}
	if got != "secret123" {
		t.Errorf("mot de passe %q", got)
	}
	if !strings.Contains(sortie.String(), "invite : ") {
		t.Error("l'invite n'a pas ete affichee")
	}
}

func TestFinDeLigneWindowsToleree(t *testing.T) {
	// Un mot de passe colle depuis Windows traine un retour chariot, qui
	// serait envoye au modem et ferait echouer l'authentification sans que
	// rien n'indique pourquoi.
	got, _ := readPasswordFrom(strings.NewReader("secret\r\n"), &strings.Builder{}, "", false)
	if got != "secret" {
		t.Errorf("%q, le retour chariot n'a pas ete retire", got)
	}
}

func TestEntreeVideNEstPasUneErreur(t *testing.T) {
	// L'appelant decide quoi faire d'un mot de passe vide ; la lecture, elle,
	// ne doit pas paniquer sur une entree fermee.
	got, err := readPasswordFrom(strings.NewReader(""), &strings.Builder{}, "", false)
	if got != "" {
		t.Errorf("attendu vide, obtenu %q", got)
	}
	_ = err
}

func TestUnTubeNEstPasUnTerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	if IsTerminal(r) {
		t.Error("un tube ne doit pas etre pris pour un terminal")
	}
}
