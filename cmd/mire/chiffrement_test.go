package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/floopich/mire/internal/settings"
	"github.com/floopich/mire/internal/store"
)

const (
	cleA = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	cleB = "1f1e1d1c1b1a191817161514131211100f0e0d0c0b0a09080706050403020100"
)

// baseChiffree cree une base dont le mot de passe du modem est chiffre.
func baseChiffree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	db, err := store.Open(filepath.Join(dir, "mire.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	k, _ := settings.ParseKey(cleA)
	settings.UseKey(k)
	defer settings.UseKey(nil)
	if err := settings.Set(ctx, db, nil, map[string]string{settings.ModemPassword: "modem-mdp"}); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.SealSecrets(ctx, db); err != nil {
		t.Fatal(err)
	}
	return dir
}

func motDePasseEnBase(t *testing.T, dir string) string {
	t.Helper()
	db, err := store.Open(filepath.Join(dir, "mire.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	v, err := store.Setting(context.Background(), db, "reglage."+settings.ModemPassword, "")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestBaseChiffreeSansCleRefusee(t *testing.T) {
	t.Cleanup(func() { settings.UseKey(nil) })
	dir := baseChiffree(t)
	t.Setenv(settings.KeyEnv, "")
	err := run([]string{"releve", "-donnees", dir})
	if err == nil || !strings.Contains(err.Error(), settings.KeyEnv) {
		t.Fatalf("relevé sans clé : %v", err)
	}
	t.Setenv(settings.KeyEnv, cleB)
	err = run([]string{"releve", "-donnees", dir})
	if err == nil || !strings.Contains(err.Error(), "ne correspond pas") {
		t.Fatalf("relevé avec une autre clé : %v", err)
	}
}

func TestDechiffrer(t *testing.T) {
	t.Cleanup(func() { settings.UseKey(nil) })
	dir := baseChiffree(t)
	args := []string{"dechiffrer", "-donnees", dir, "-ecoute", "127.0.0.1:1"}

	t.Setenv(settings.KeyEnv, "")
	if err := run(args); err == nil || !strings.Contains(err.Error(), "clé actuelle") {
		t.Fatalf("sans clé : %v", err)
	}
	t.Setenv(settings.KeyEnv, cleB)
	if err := run(args); err == nil {
		t.Fatal("autre clé acceptée")
	}
	if v := motDePasseEnBase(t, dir); !strings.HasPrefix(v, "mire-chiffre-v1:") {
		t.Fatalf("base modifiée par un refus : %q", v)
	}
	t.Setenv(settings.KeyEnv, cleA)
	if err := run(args); err != nil {
		t.Fatal(err)
	}
	if v := motDePasseEnBase(t, dir); v != "modem-mdp" {
		t.Fatalf("après déchiffrement : %q", v)
	}
}
