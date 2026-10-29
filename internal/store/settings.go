package store

import (
	"context"
	"database/sql"
)

// Setting lit un reglage, ou renvoie la valeur par defaut s'il est absent.
func Setting(ctx context.Context, db *sql.DB, key, fallback string) (string, error) {
	var v string
	err := db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return fallback, nil
	}
	if err != nil {
		return fallback, err
	}
	return v, nil
}

// SetSetting enregistre un reglage.
func SetSetting(ctx context.Context, db *sql.DB, key, value string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO settings(key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// AddSetting enregistre un reglage seulement s'il n'existe pas encore, en une
// seule instruction, et dit s'il l'a fait.
func AddSetting(ctx context.Context, db *sql.DB, key, value string) (bool, error) {
	res, err := db.ExecContext(ctx,
		`INSERT INTO settings(key, value) VALUES (?, ?) ON CONFLICT(key) DO NOTHING`, key, value)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}
