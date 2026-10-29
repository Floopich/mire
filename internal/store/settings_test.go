package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestReglageAbsentRenvoieLeDefaut(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if v, _ := Setting(context.Background(), db, "inexistant", "repli"); v != "repli" {
		t.Errorf("%q, attendu le repli", v)
	}
}

func TestReglageEnregistreEtRelu(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()

	if err := SetSetting(ctx, db, "cle", "un"); err != nil {
		t.Fatalf("ecriture: %v", err)
	}
	// Une deuxieme ecriture remplace la premiere plutot que d'echouer.
	if err := SetSetting(ctx, db, "cle", "deux"); err != nil {
		t.Fatalf("mise a jour: %v", err)
	}
	if v, _ := Setting(ctx, db, "cle", ""); v != "deux" {
		t.Errorf("%q, attendu deux", v)
	}
}

func TestLAncienReglageOFDMAEstRepris(t *testing.T) {
	// Une instance anterieure a la page Reglages gardait ce choix sous un
	// autre nom : la migration 3 doit le conserver.
	db, err := Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	if err := SetSetting(ctx, db, "ofdma_low_qam_expected", "true"); err != nil {
		t.Fatal(err)
	}
	// La migration 3 rejouee telle quelle sur une base qui porte l'ancienne cle.
	sqlText, err := migrationFS.ReadFile("migrations/0003_reglages.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(sqlText)); err != nil {
		t.Fatalf("migration: %v", err)
	}
	if v, _ := Setting(ctx, db, "reglage.analyse.ofdma_basse", ""); v != "true" {
		t.Errorf("reglage repris : %q, attendu true", v)
	}
	if v, _ := Setting(ctx, db, "ofdma_low_qam_expected", "absent"); v != "absent" {
		t.Errorf("l'ancienne cle subsiste : %q", v)
	}
}

func TestLAncienInterrupteurOFDMADevientUnPlancher(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	if err := SetSetting(ctx, db, "reglage.analyse.ofdma_basse", "true"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 5"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if v, _ := Setting(ctx, db, "reglage.analyse.ofdma_plancher", ""); v != "16" {
		t.Errorf("plancher %q, attendu 16", v)
	}
	if v, _ := Setting(ctx, db, "reglage.analyse.ofdma_basse", "absent"); v != "absent" {
		t.Error("l'ancien interrupteur subsiste")
	}
}
