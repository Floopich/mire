package store

import (
	"context"
	"database/sql"
	"errors"
)

// Backup ecrit une copie coherente de la base dans path, pendant que Mire
// tourne : VACUUM INTO lit une image figee de la base et l'ecrit compactee.
// Le fichier ne doit pas exister.
func Backup(ctx context.Context, db *sql.DB, path string) error {
	_, err := db.ExecContext(ctx, `VACUUM INTO ?`, path)
	return err
}

// BackupWithout ecrit une copie comme Backup, sans les reglages nommes ni
// les sessions. secure_delete et VACUUM effacent aussi les valeurs retirees
// des pages libres du fichier.
func BackupWithout(ctx context.Context, db *sql.DB, path string, keys []string) error {
	if err := Backup(ctx, db, path); err != nil {
		return err
	}
	c, err := sql.Open(driverName, path)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	c.SetMaxOpenConns(1)
	if _, err := c.ExecContext(ctx, `PRAGMA secure_delete = ON`); err != nil {
		return err
	}
	if _, err := c.ExecContext(ctx, `DELETE FROM sessions`); err != nil {
		return err
	}
	for _, k := range keys {
		if _, err := c.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, k); err != nil {
			return err
		}
	}
	_, err = c.ExecContext(ctx, `VACUUM`)
	return err
}

// Validate verifie qu'un fichier est une base de Mire intacte, sans la
// modifier : une base SQLite d'un autre programme, ou abimee, est refusee.
// Les migrations d'une base plus ancienne s'appliqueront a son ouverture.
func Validate(ctx context.Context, path string) error {
	db, err := sql.Open(driverName, dsn(path))
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	var version int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return errors.New("ce n'est pas une base SQLite")
	}
	var readings int
	if version < 1 || db.QueryRowContext(ctx, `SELECT count(*) FROM readings`).Scan(&readings) != nil {
		return errors.New("ce n'est pas une base de Mire")
	}
	var check string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&check); err != nil || check != "ok" {
		return errors.New("la base est abîmée")
	}
	return nil
}
