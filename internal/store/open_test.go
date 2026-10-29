package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOpenPoseLesPragmasEtMigre(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatalf("ouverture: %v", err)
	}
	defer func() { _ = db.Close() }()

	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, attendu wal", mode)
	}

	// Sans foreign_keys, les ON DELETE CASCADE du schema ne s'appliquent pas :
	// purger un releve laisserait ses canaux orphelins.
	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Error("foreign_keys doit etre actif")
	}

	version, err := schemaVersion(db)
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if want, _ := CodeVersion(); version != want {
		t.Errorf("schema en version %d, attendu %d", version, want)
	}
}

func TestSuppressionDUnReleveEmporteSesCanaux(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatalf("ouverture: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec(`INSERT INTO readings(id, ts, docsis) VALUES (1, 100, '3.1')`); err != nil {
		t.Fatalf("insertion releve: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO ds_channels(reading_id, ts, channel_id, power_dbmv) VALUES (1, 100, 7, 2.5)`,
	); err != nil {
		t.Fatalf("insertion canal: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM readings WHERE id = 1`); err != nil {
		t.Fatalf("suppression: %v", err)
	}

	var orphelins int
	if err := db.QueryRow("SELECT count(*) FROM ds_channels").Scan(&orphelins); err != nil {
		t.Fatalf("comptage: %v", err)
	}
	if orphelins != 0 {
		t.Errorf("%d canaux orphelins apres purge du releve", orphelins)
	}
}

func TestLesPragmasValentPourChaqueConnexion(t *testing.T) {
	// database/sql ouvre plusieurs connexions : un PRAGMA pose une seule fois
	// ne vaudrait que pour l'une d'elles, et les cascades seraient aleatoires.
	db, err := Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatalf("ouverture: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	var conns []*sql.Conn
	for range 3 {
		c, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("connexion: %v", err)
		}
		conns = append(conns, c)
	}
	for i, c := range conns {
		var fk int
		if err := c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
			t.Fatalf("connexion %d: %v", i, err)
		}
		if fk != 1 {
			t.Errorf("connexion %d : foreign_keys inactif", i)
		}
		_ = c.Close()
	}
}
