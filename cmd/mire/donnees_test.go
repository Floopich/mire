package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/store"
)

// sauvegarde ecrit une base d'un releve et renvoie le chemin de sa copie.
func sauvegarde(t *testing.T, canal int) string {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	raw := docsis.RawSnapshot{Downstream: []docsis.RawDownstream{{ChannelID: docsis.FlexInt(canal), Frequency: "442 MHz", PowerLevel: "2"}}}
	if _, err := store.SaveSnapshot(ctx, db, raw.Normalize(time.Now())); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "sauvegarde.db")
	if err := store.Backup(ctx, db, out); err != nil {
		t.Fatal(err)
	}
	return out
}

func restaurer(dir, src string) error {
	return run([]string{"restauration", "-donnees", dir, "-ecoute", "127.0.0.1:1", src})
}

func canalRestaure(t *testing.T, dir string) int {
	t.Helper()
	db, err := store.Open(filepath.Join(dir, "mire.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	snap, err := store.LatestSnapshot(context.Background(), db)
	if err != nil || len(snap.Downstream) != 1 {
		t.Fatalf("relevé restauré : %+v, %v", snap, err)
	}
	return snap.Downstream[0].ChannelID
}

func TestRestauration(t *testing.T) {
	dir := t.TempDir()
	if err := restaurer(dir, sauvegarde(t, 7)); err != nil {
		t.Fatal(err)
	}
	if got := canalRestaure(t, dir); got != 7 {
		t.Fatalf("canal %d, attendu 7", got)
	}
	// Windows n'a pas de droits Unix : seule la presence se verifie.
	if info, err := os.Stat(filepath.Join(dir, "mire.db")); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Errorf("droits de la base : %v, %v", info, err)
	}

	// Une seconde restauration garde la base remplacee a cote.
	if err := restaurer(dir, sauvegarde(t, 9)); err != nil {
		t.Fatal(err)
	}
	if got := canalRestaure(t, dir); got != 9 {
		t.Fatalf("canal %d, attendu 9", got)
	}
	kept, _ := filepath.Glob(filepath.Join(dir, "mire.db.avant-restauration-*"))
	if len(kept) == 0 {
		t.Error("l'ancienne base n'est pas conservée")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".restauration-*")); len(left) != 0 {
		t.Errorf("fichiers temporaires restés : %v", left)
	}
}

func TestRestaurationRefusee(t *testing.T) {
	texte := filepath.Join(t.TempDir(), "texte.db")
	if err := os.WriteFile(texte, []byte("pas une base"), 0o600); err != nil {
		t.Fatal(err)
	}
	vide := filepath.Join(t.TempDir(), "vide.db")
	if err := os.WriteFile(vide, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for nom, src := range map[string]string{"texte": texte, "vide": vide} {
		dir := t.TempDir()
		err := restaurer(dir, src)
		if err == nil || !strings.Contains(err.Error(), "sauvegarde refusée") {
			t.Errorf("%s : %v", nom, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "mire.db")); err == nil {
			t.Errorf("%s : une base a été écrite malgré le refus", nom)
		}
	}
	if err := run([]string{"restauration", "-donnees", t.TempDir()}); err == nil {
		t.Error("restauration sans fichier acceptée")
	}
}

func TestDossierNonAccessible(t *testing.T) {
	if os.Getuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("root écrit partout, et Windows ignore les droits Unix : le refus ne se constate pas")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	err := checkWritable(dir)
	if err == nil || !strings.Contains(err.Error(), "chown") {
		t.Fatalf("erreur %v, attendu la marche à suivre", err)
	}
}
