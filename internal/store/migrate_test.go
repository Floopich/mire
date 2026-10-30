package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func openTemp(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open(driverName, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("ouverture: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMigrationCreeLeSchema(t *testing.T) {
	db := openTemp(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("migration: %v", err)
	}
	for _, table := range []string{"readings", "ds_channels", "us_channels", "link_events", "settings"} {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err != nil {
			t.Errorf("table %s absente: %v", table, err)
		}
	}
	got, err := schemaVersion(db)
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	want, _ := CodeVersion()
	if got != want {
		t.Errorf("version enregistree %d, attendu %d", got, want)
	}
}

func TestMigrationEstIdempotente(t *testing.T) {
	db := openTemp(t)
	for i := range 3 {
		if err := Migrate(db); err != nil {
			t.Fatalf("passage %d: %v", i+1, err)
		}
	}
}

func TestBaseTropRecenteEstRefusee(t *testing.T) {
	db := openTemp(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("migration: %v", err)
	}
	// Une base ecrite par une version future du programme.
	if _, err := db.Exec("PRAGMA user_version = 9999"); err != nil {
		t.Fatalf("preparation: %v", err)
	}
	err := Migrate(db)
	if err == nil {
		t.Fatal("une base plus recente doit etre refusee, pas migree")
	}
	if !strings.Contains(err.Error(), "9999") {
		t.Errorf("le message doit citer la version trouvee: %q", err)
	}
}

func TestLesIndexAttendusExistent(t *testing.T) {
	db := openTemp(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("migration: %v", err)
	}
	// Sans (channel_id, ts), une courbe sur dix ans redevient un balayage.
	for _, index := range []string{"idx_ds_channel_ts", "idx_us_channel_ts", "idx_readings_ts"} {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='index' AND name=?", index).Scan(&name)
		if err != nil {
			t.Errorf("index %s absent: %v", index, err)
		}
	}
}

func TestLaRequeteDHistoriqueUtiliseLIndex(t *testing.T) {
	db := openTemp(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("migration: %v", err)
	}
	rows, err := db.Query(`EXPLAIN QUERY PLAN
		SELECT ts, power_dbmv FROM ds_channels
		WHERE channel_id = ? AND ts BETWEEN ? AND ? ORDER BY ts`, 1, 0, 1)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var plan string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("lecture du plan: %v", err)
		}
		plan += detail + "\n"
	}
	if !strings.Contains(plan, "idx_ds_channel_ts") {
		t.Errorf("l'index n'est pas utilise, plan obtenu:\n%s", plan)
	}
}

func TestNumerosDeMigrationUniquesEtOrdonnes(t *testing.T) {
	all, err := loadMigrations()
	if err != nil {
		t.Fatalf("chargement: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("aucune migration")
	}
	for i := 1; i < len(all); i++ {
		if all[i].version <= all[i-1].version {
			t.Errorf("%s et %s ne sont pas strictement croissants", all[i-1].name, all[i].name)
		}
	}
}

func TestUneBaseEstCopieeAvantSaMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mire.db")
	latest, _ := CodeVersion()
	all, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	ancienne, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range all {
		if m.version < latest {
			if err := applyMigration(ancienne, m); err != nil {
				t.Fatal(err)
			}
		}
	}
	_ = ancienne.Close()
	// Une copie plus ancienne est remplacee par la nouvelle.
	if err := os.WriteFile(path+".avant-v1", []byte("vieux"), 0o600); err != nil {
		t.Fatal(err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	copie := fmt.Sprintf("%s.avant-v%d", path, latest-1)
	info, err := os.Stat(copie)
	if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Fatalf("copie avant migration : %v %v", info, err)
	}
	if _, err := os.Stat(path + ".avant-v1"); !os.IsNotExist(err) {
		t.Error("l'ancienne copie doit être retirée")
	}
	v, err := sql.Open(driverName, copie)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = v.Close() }()
	if got, _ := schemaVersion(v); got != latest-1 {
		t.Errorf("la copie est en version %d, attendu %d", got, latest-1)
	}

	// Une base neuve n'a rien a copier.
	neuve := filepath.Join(t.TempDir(), "neuve.db")
	db, err = Open(neuve)
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if m, _ := filepath.Glob(neuve + ".avant-v*"); len(m) != 0 {
		t.Errorf("copie inattendue : %v", m)
	}
}
