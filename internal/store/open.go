package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Open ouvre la base locale et la met au schema attendu. Les pragmas (WAL,
// foreign_keys) sont poses par la chaine de connexion, pour chaque
// connexion du pool.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open(driverName, dsn(path))
	if err != nil {
		return nil, fmt.Errorf("ouverture de %s: %w", path, err)
	}
	// Un seul ecrivain a la fois de toute facon dans SQLite : quelques
	// connexions suffisent aux lectures concurrentes de l'interface.
	db.SetMaxOpenConns(4)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ouverture de %s: %w", path, err)
	}
	if err := backupBeforeMigration(db, path); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := Migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// backupBeforeMigration copie une base existante avant qu'une nouvelle
// version de Mire en change le schema : path.avant-vN, N etant la version du
// schema copie. Une copie deja presente pour cette version est gardee ; les
// plus anciennes sont retirees. Sans place pour la copie, rien n'est migre.
func backupBeforeMigration(db *sql.DB, path string) error {
	current, err := schemaVersion(db)
	if err != nil {
		return err
	}
	latest, err := CodeVersion()
	if err != nil {
		return err
	}
	if current == 0 || current >= latest {
		return nil
	}
	target := fmt.Sprintf("%s.avant-v%d", path, current)
	if _, err := os.Stat(target); errors.Is(err, fs.ErrNotExist) {
		if err := Backup(context.Background(), db, target); err != nil {
			_ = os.Remove(target)
			return fmt.Errorf("copie de la base avant mise à jour impossible (%w) : libérez de la place, puis relancez Mire", err)
		}
		// Elle contient le mot de passe du modem, comme la base.
		if err := os.Chmod(target, 0o600); err != nil {
			return err
		}
	}
	old, _ := filepath.Glob(path + ".avant-v*")
	for _, f := range old {
		if f != target {
			_ = os.Remove(f)
		}
	}
	return nil
}
