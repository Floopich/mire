// Package store porte la base locale et ses migrations.
package store

import (
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations lit les migrations embarquees (NNNN_description.sql),
// triees par numero. Un numero duplique est une erreur.
func loadMigrations() ([]migration, error) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	var out []migration
	seen := map[int]string{}
	for _, e := range entries {
		name := e.Name()
		prefix, _, found := strings.Cut(strings.TrimSuffix(name, ".sql"), "_")
		if !found {
			return nil, fmt.Errorf("migration %q: nom attendu NNNN_description.sql", name)
		}
		version, err := strconv.Atoi(prefix)
		if err != nil {
			return nil, fmt.Errorf("migration %q: numero illisible", name)
		}
		if other, dup := seen[version]; dup {
			return nil, fmt.Errorf("migrations %q et %q portent le meme numero", other, name)
		}
		seen[version] = name
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: version, name: name, sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// CodeVersion est la version de schema que ce binaire sait produire.
func CodeVersion() (int, error) {
	all, err := loadMigrations()
	if err != nil {
		return 0, err
	}
	if len(all) == 0 {
		return 0, nil
	}
	return all[len(all)-1].version, nil
}

// schemaVersion lit la version enregistree (PRAGMA user_version).
func schemaVersion(db *sql.DB) (int, error) {
	var v int
	err := db.QueryRow("PRAGMA user_version").Scan(&v)
	return v, err
}

// Migrate amene la base au schema attendu par ce binaire.
//
// Refuse une base venant d'une version plus recente.
func Migrate(db *sql.DB) error {
	all, err := loadMigrations()
	if err != nil {
		return err
	}
	current, err := schemaVersion(db)
	if err != nil {
		return fmt.Errorf("lecture de la version du schema: %w", err)
	}
	latest, err := CodeVersion()
	if err != nil {
		return err
	}
	if current > latest {
		return fmt.Errorf(
			"la base est en version %d, ce programme ne connaît que la %d : "+
				"utilisez une version plus récente ou restaurez une sauvegarde",
			current, latest)
	}
	for _, m := range all {
		if m.version <= current {
			continue
		}
		if err := applyMigration(db, m); err != nil {
			return err
		}
	}
	return nil
}

// applyMigration joue une migration et sa version dans la meme transaction.
func applyMigration(db *sql.DB, m migration) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(m.sql); err != nil {
		return fmt.Errorf("migration %s: %w", m.name, err)
	}
	// PRAGMA n'accepte pas de parametre lie ; le numero vient d'un Atoi, donc
	// d'un entier, jamais d'une saisie.
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
		return fmt.Errorf("migration %s: enregistrement de la version: %w", m.name, err)
	}
	return tx.Commit()
}
