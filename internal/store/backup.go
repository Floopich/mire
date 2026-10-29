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
