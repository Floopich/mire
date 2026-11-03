package settings

import (
	"errors"
	"strings"
	"testing"
)

func TestSecretsParFichier(t *testing.T) {
	files := map[string]string{"/run/secrets/modem": "s3cret\n", "/vide": " \n"}
	read := func(p string) ([]byte, error) {
		if v, ok := files[p]; ok {
			return []byte(v), nil
		}
		return nil, errors.New("absent")
	}
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	s, err := SecretFiles(env(map[string]string{"MIRE_MODEM_MOT_DE_PASSE_FICHIER": "/run/secrets/modem"}), read)
	if err != nil || s[ModemPassword] != "s3cret" {
		t.Fatalf("lecture : %v %v", s, err)
	}
	// Rien de demande, rien de lu : l'installation ordinaire n'est pas touchee.
	if s, err := SecretFiles(env(nil), read); err != nil || len(s) != 0 {
		t.Errorf("sans fichier : %v %v", s, err)
	}
	for _, c := range []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"MIRE_MODEM_MOT_DE_PASSE": "x", "MIRE_MODEM_MOT_DE_PASSE_FICHIER": "/run/secrets/modem"}, "choisissez l'un ou l'autre"},
		{map[string]string{"MIRE_MODEM_MOT_DE_PASSE_FICHIER": "/absent"}, "illisible"},
		{map[string]string{"MIRE_MODEM_MOT_DE_PASSE_FICHIER": "/vide"}, "vide"},
	} {
		_, err := SecretFiles(env(c.env), read)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v : %v, attendu %q", c.env, err, c.want)
		}
		// Le message nomme la variable, jamais le secret.
		if err != nil && strings.Contains(err.Error(), "s3cret") {
			t.Error("secret dans le message d'erreur")
		}
	}
	// Seuls les secrets se lisent par fichier.
	if s, _ := SecretFiles(env(map[string]string{"MIRE_MODEM_FICHIER": "/run/secrets/modem"}), read); len(s) != 0 {
		t.Error("un réglage ordinaire lu par fichier")
	}
}
