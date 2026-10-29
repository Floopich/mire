package store

import (
	"database/sql"
	"fmt"
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
	if err := Migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}
