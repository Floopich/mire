package settings

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/floopich/mire/internal/store"
)

const (
	cleA = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	cleB = "1f1e1d1c1b1a191817161514131211100f0e0d0c0b0a09080706050403020100"
)

func avecCle(t *testing.T, hex string) {
	t.Helper()
	if hex == "" {
		UseKey(nil)
		return
	}
	k, err := ParseKey(hex)
	if err != nil {
		t.Fatal(err)
	}
	UseKey(k)
	t.Cleanup(func() { UseKey(nil) })
}

func brut(t *testing.T, db *sql.DB, k string) string {
	t.Helper()
	v, err := store.Setting(context.Background(), db, key(k), "")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestParseKey(t *testing.T) {
	for _, ok := range []string{cleA, " " + cleA + "\n", "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="} {
		if _, err := ParseKey(ok); err != nil {
			t.Errorf("%q refusée : %v", ok, err)
		}
	}
	for _, ko := range []string{"", "motdepasse", cleA[:62], cleA + "00", "zz" + cleA[2:]} {
		if _, err := ParseKey(ko); !errors.Is(err, ErrKeyFormat) {
			t.Errorf("%q acceptée", ko)
		}
	}
}

func TestKeyFrom(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	lire := func(p string) ([]byte, error) {
		if p == "/run/secrets/cle" {
			return []byte(cleA + "\n"), nil
		}
		return nil, os.ErrNotExist
	}
	if k, err := KeyFrom(env(nil), lire); k != nil || err != nil {
		t.Errorf("sans clé : %v, %v", k, err)
	}
	if k, err := KeyFrom(env(map[string]string{KeyEnv: cleA}), lire); k == nil || err != nil {
		t.Errorf("variable : %v", err)
	}
	if k, err := KeyFrom(env(map[string]string{KeyEnv + FileSuffix: "/run/secrets/cle"}), lire); k == nil || err != nil {
		t.Errorf("fichier : %v", err)
	}
	if _, err := KeyFrom(env(map[string]string{KeyEnv: cleA, KeyEnv + FileSuffix: "/run/secrets/cle"}), lire); err == nil {
		t.Error("deux formes acceptées")
	}
	if _, err := KeyFrom(env(map[string]string{KeyEnv + FileSuffix: "/absent"}), lire); err == nil {
		t.Error("fichier absent accepté")
	}
	if _, err := KeyFrom(env(map[string]string{KeyEnv: "phrase de passe"}), lire); err == nil || !strings.Contains(err.Error(), KeyEnv) {
		t.Errorf("clé invalide : %v", err)
	}
}

func TestSecretsEnClairSansCle(t *testing.T) {
	avecCle(t, "")
	db := base(t)
	ctx := context.Background()
	if err := Set(ctx, db, nil, map[string]string{ModemPassword: "secret"}); err != nil {
		t.Fatal(err)
	}
	if got := brut(t, db, ModemPassword); got != "secret" {
		t.Errorf("enregistré %q", got)
	}
	if err := CheckSecrets(ctx, db); err != nil {
		t.Error(err)
	}
	if n, err := SealSecrets(ctx, db); n != 0 || err != nil {
		t.Errorf("SealSecrets sans clé : %d, %v", n, err)
	}
	if Sealed(ctx, db) {
		t.Error("base marquée chiffrée")
	}
}

func TestChiffrementDesSecrets(t *testing.T) {
	db := base(t)
	ctx := context.Background()

	// Secrets enregistres avant la cle, en clair.
	avecCle(t, "")
	if err := Set(ctx, db, nil, map[string]string{ModemPassword: "modem-mdp", NtfyTopic: "sujet-prive", Interval: "15m"}); err != nil {
		t.Fatal(err)
	}

	avecCle(t, cleA)
	if err := CheckSecrets(ctx, db); err != nil {
		t.Fatalf("base en clair refusée avec une clé : %v", err)
	}
	n, err := SealSecrets(ctx, db)
	if err != nil || n != 2 {
		t.Fatalf("SealSecrets : %d, %v ; attendu 2", n, err)
	}
	for _, k := range []string{ModemPassword, NtfyTopic} {
		if v := brut(t, db, k); !strings.HasPrefix(v, sealPrefix) || strings.Contains(v, "mdp") || strings.Contains(v, "sujet") {
			t.Errorf("%s en base : %q", k, v)
		}
	}
	if v := brut(t, db, Interval); v != "15m" {
		t.Errorf("réglage ordinaire modifié : %q", v)
	}
	if !Sealed(ctx, db) {
		t.Error("ligne de contrôle absente")
	}
	if n, err := SealSecrets(ctx, db); n != 0 || err != nil {
		t.Errorf("second passage : %d, %v", n, err)
	}

	// Lecture et nouvel enregistrement, chiffre directement.
	v, err := Load(ctx, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v.String(ModemPassword) != "modem-mdp" || v.String(NtfyTopic) != "sujet-prive" {
		t.Errorf("valeurs lues : %q, %q", v.String(ModemPassword), v.String(NtfyTopic))
	}
	if err := Set(ctx, db, nil, map[string]string{MQTTPassword: "mqtt-mdp"}); err != nil {
		t.Fatal(err)
	}
	if v := brut(t, db, MQTTPassword); !strings.HasPrefix(v, sealPrefix) {
		t.Errorf("nouveau secret en clair : %q", v)
	}

	// Une valeur recopiee sur une autre ligne ne se dechiffre pas.
	if _, err := db.Exec(`UPDATE settings SET value = ? WHERE key = ?`, brut(t, db, ModemPassword), key(MQTTPassword)); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(ctx, db, nil); err == nil {
		t.Error("secret déplacé accepté")
	}
	if err := Set(ctx, db, nil, map[string]string{MQTTPassword: "mqtt-mdp"}); err != nil {
		t.Fatal(err)
	}

	// Sans cle, ou avec une autre : refus net.
	avecCle(t, "")
	if err := CheckSecrets(ctx, db); err == nil || !strings.Contains(err.Error(), KeyEnv) {
		t.Errorf("sans clé : %v", err)
	}
	if _, err := Load(ctx, db, nil); err == nil {
		t.Error("Load sans clé accepté")
	}
	avecCle(t, cleB)
	if err := CheckSecrets(ctx, db); err == nil || !strings.Contains(err.Error(), "ne correspond pas") {
		t.Errorf("autre clé : %v", err)
	}
	if _, err := UnsealSecrets(ctx, db); err == nil {
		t.Error("déchiffrement avec une autre clé accepté")
	}

	// Retour en clair avec la bonne cle.
	avecCle(t, cleA)
	if n, err := UnsealSecrets(ctx, db); err != nil || n != 3 {
		t.Fatalf("UnsealSecrets : %d, %v ; attendu 3", n, err)
	}
	avecCle(t, "")
	if err := CheckSecrets(ctx, db); err != nil {
		t.Errorf("base déchiffrée refusée sans clé : %v", err)
	}
	if v := brut(t, db, ModemPassword); v != "modem-mdp" {
		t.Errorf("après déchiffrement : %q", v)
	}
	if Sealed(ctx, db) {
		t.Error("ligne de contrôle restée")
	}
}

func TestSecretChiffreSansControle(t *testing.T) {
	// Une copie qui aurait perdu sa ligne de controle garde des secrets
	// chiffres : sans cle, elle est refusee aussi.
	db := base(t)
	ctx := context.Background()
	avecCle(t, cleA)
	if err := Set(ctx, db, nil, map[string]string{ModemPassword: "x"}); err != nil {
		t.Fatal(err)
	}
	avecCle(t, "")
	if err := CheckSecrets(ctx, db); err == nil {
		t.Error("secret chiffré sans contrôle accepté sans clé")
	}
}

func TestEffacerUnSecretChiffre(t *testing.T) {
	db := base(t)
	ctx := context.Background()
	avecCle(t, cleA)
	if err := Set(ctx, db, nil, map[string]string{ModemPassword: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := Clear(ctx, db, nil, []string{ModemPassword}); err != nil {
		t.Fatal(err)
	}
	v, err := Load(ctx, db, nil)
	if err != nil || v.String(ModemPassword) != "" {
		t.Errorf("après effacement : %q, %v", v.String(ModemPassword), err)
	}
}
