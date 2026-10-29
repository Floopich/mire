package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSauvegardeCoherente(t *testing.T) {
	db := baseTest(t)
	ctx := context.Background()
	at := time.Unix(1_700_000_000, 0).UTC()
	for i := range 3 {
		if _, err := SaveSnapshot(ctx, db, exemple(at.Add(time.Duration(i)*time.Minute))); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(t.TempDir(), "copie.db")
	if err := Backup(ctx, db, target); err != nil {
		t.Fatal(err)
	}
	copie, err := Open(target)
	if err != nil {
		t.Fatalf("la sauvegarde ne s'ouvre pas : %v", err)
	}
	defer func() { _ = copie.Close() }()
	var n int
	if err := copie.QueryRow("SELECT count(*) FROM readings").Scan(&n); err != nil || n != 3 {
		t.Errorf("%d releves dans la copie (%v), attendu 3", n, err)
	}
	if err := Backup(ctx, db, target); err == nil {
		t.Error("une sauvegarde ne doit pas ecraser un fichier existant")
	}
}

func TestNotes(t *testing.T) {
	db := baseTest(t)
	ctx := context.Background()
	at := time.Unix(1_700_000_000, 0).UTC()
	if err := AddNote(ctx, db, at, "Technicien passé"); err != nil {
		t.Fatal(err)
	}
	if err := SaveEvents(ctx, db, []Event{{Time: at, Kind: "etat", Message: "releve"}}); err != nil {
		t.Fatal(err)
	}
	list, err := Events(ctx, db, EventFilter{From: at.Add(-time.Hour), To: at.Add(time.Hour)})
	if err != nil || len(list) != 2 {
		t.Fatalf("%d evenements (%v)", len(list), err)
	}
	for _, e := range list {
		ok, err := DeleteNote(ctx, db, e.ID)
		if err != nil {
			t.Fatal(err)
		}
		if ok != (e.Kind == NoteKind) {
			t.Errorf("%s supprime : %v", e.Kind, ok)
		}
	}
}
