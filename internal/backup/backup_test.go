package backup

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/floopich/mire/internal/settings"
	"github.com/floopich/mire/internal/store"
)

func TestUneCopieParJourSansSecrets(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "mire.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := settings.Set(ctx, db, settings.Source{}, map[string]string{settings.ModemPassword: "secret-temoin"}); err != nil {
		t.Fatal(err)
	}
	// Un fichier de l'utilisateur dans le dossier n'est jamais touche.
	if err := os.MkdirAll(filepath.Join(dir, Dir), 0o700); err != nil {
		t.Fatal(err)
	}
	autre := filepath.Join(dir, Dir, "ma-copie.db")
	if err := os.WriteFile(autre, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 10, 1, 3, 0, 0, 0, time.Local)
	k := &Keeper{DB: db, DataDir: dir, Keep: 3, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return now }}
	for range 5 {
		if err := k.Once(ctx); err != nil {
			t.Fatal(err)
		}
		// Une seconde verification dans l'heure n'ecrit rien.
		now = now.Add(time.Hour)
		if err := k.Once(ctx); err != nil {
			t.Fatal(err)
		}
		now = now.Add(23 * time.Hour)
	}
	copies, err := List(dir)
	if err != nil || len(copies) != 3 {
		t.Fatalf("%d copies, %v", len(copies), err)
	}
	if want := time.Date(2026, 10, 5, 3, 0, 0, 0, time.Local); !copies[0].Time.Equal(want) {
		t.Errorf("dernière copie %v, attendue %v", copies[0].Time, want)
	}
	for _, c := range copies {
		b, err := os.ReadFile(c.Path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(b, []byte("secret-temoin")) {
			t.Errorf("%s contient le mot de passe du modem", c.Path)
		}
		if err := store.Validate(ctx, c.Path); err != nil {
			t.Errorf("%s : %v", c.Path, err)
		}
	}
	if _, err := os.Stat(autre); err != nil {
		t.Error("le fichier de l'utilisateur a disparu")
	}
}

func TestUneCopieInterrompueEstRetiree(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, Dir), 0o700); err != nil {
		t.Fatal(err)
	}
	reste := filepath.Join(dir, Dir, partial+"abc.db")
	if err := os.WriteFile(reste, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	(&Keeper{DataDir: dir}).cleanPartial()
	if _, err := os.Stat(reste); !os.IsNotExist(err) {
		t.Error("la copie interrompue doit être retirée")
	}
}
