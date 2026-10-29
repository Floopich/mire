package main

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/floopich/mire/internal/modem"
	"github.com/floopich/mire/internal/operator"
	"github.com/floopich/mire/internal/settings"
)

func env(values map[string]string) func(string) string {
	return func(k string) string { return values[k] }
}

func TestValeursParDefaut(t *testing.T) {
	c, err := parseConfig("mire", nil, env(nil), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "0.0.0.0:1340" || c.DataDir != "." || len(c.Forced) != 0 {
		t.Errorf("defauts inattendus: %+v", c)
	}
}

func TestLOptionLEmporteSurLEnvironnement(t *testing.T) {
	c, err := parseConfig("mire", []string{"-intervalle", "5m"},
		env(map[string]string{"MIRE_INTERVALLE": "15m", "MIRE_DONNEES": "/data", "MIRE_PROFIL": "voo"}), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if c.Forced[settings.Interval] != "5m" {
		t.Errorf("intervalle %q, attendu 5m", c.Forced[settings.Interval])
	}
	if c.Forced[settings.Profile] != "voo" || c.DataDir != "/data" {
		t.Errorf("environnement mal lu : %+v", c)
	}
}

func TestReglagesInvalidesRefuses(t *testing.T) {
	cas := []map[string]string{
		{"MIRE_INTERVALLE": "abc"},
		{"MIRE_INTERVALLE": "10s"},
		{"MIRE_RETENTION": "-1"},
		{"MIRE_TAILLE_MAX": "x"},
		{"MIRE_PROFIL": "inexistant"},
		{"MIRE_MODEM": "ftp://192.168.100.1"},
	}
	for _, c := range cas {
		if _, err := parseConfig("mire", nil, env(c), io.Discard); err == nil {
			t.Errorf("%v : erreur attendue", c)
		}
	}
}

func TestLeMotDePasseDuModemNeSePasseJamaisEnOption(t *testing.T) {
	c, err := parseConfig("mire", nil, env(map[string]string{"MIRE_MODEM_MOT_DE_PASSE": "secret"}), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if c.Forced[settings.ModemPassword] != "secret" {
		t.Error("mot de passe du modem non lu")
	}
	if _, err := parseConfig("mire", []string{"-mot-de-passe", "x"}, env(nil), io.Discard); err == nil {
		t.Error("une option de mot de passe ne doit pas exister")
	}
}

func TestPortDeSante(t *testing.T) {
	for in, want := range map[string]string{
		"0.0.0.0:1340": "http://127.0.0.1:1340/sante",
		":8080":        "http://127.0.0.1:8080/sante",
		"[::]:1340":    "http://127.0.0.1:1340/sante",
	} {
		got, err := healthURL(in)
		if err != nil || got != want {
			t.Errorf("%s -> %s (%v), attendu %s", in, got, err, want)
		}
	}
}

func TestChaqueProfilDesigneUnModeleConnu(t *testing.T) {
	profils, err := operator.All()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range profils {
		if p.DefaultModem == "" {
			continue
		}
		if _, err := modem.Lookup(p.DefaultModem); err != nil {
			t.Errorf("profil %s : %v", p.ID, err)
		}
	}
}

func TestArgumentsApresLesOptions(t *testing.T) {
	_, rest, err := parseConfigArgs("mire sauvegarde", []string{"-donnees", "/data", "/data/copie.db"}, env(nil), io.Discard)
	if err != nil || len(rest) != 1 || rest[0] != "/data/copie.db" {
		t.Errorf("%v %v", rest, err)
	}
	if _, err := parseConfig("mire", []string{"en-trop"}, env(nil), io.Discard); err == nil {
		t.Error("un argument libre doit etre refuse hors de mire sauvegarde")
	}
}

func TestCommandeSauvegarde(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "copie.db")
	if err := run([]string{"sauvegarde", "-donnees", dir, target}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("droits %v : la copie contient le mot de passe du modem", info.Mode().Perm())
	}
	if err := run([]string{"sauvegarde", "-donnees", dir, target}); err == nil {
		t.Error("une sauvegarde ne doit pas ecraser un fichier existant")
	}
	if err := run([]string{"sauvegarde", "-donnees", dir}); err == nil {
		t.Error("sans destination, la commande doit le dire")
	}
}
