package store

import (
	"database/sql"
	"fmt"
)

// Open ouvre la base locale et la met au schema attendu.
//
// Les pragmas sont poses par la chaine de connexion (voir dsn) plutot que dans
// une migration : ils decrivent la facon dont ce programme veut utiliser le
// fichier, pas le schema. WAL laisse les lectures se poursuivre pendant qu'un
// releve s'ecrit, ce qui compte sur une carte SD ou l'ecriture est lente.
// foreign_keys est desactive par defaut dans SQLite et doit etre demande a
// chaque connexion, sans quoi les ON DELETE CASCADE du schema ne
// s'appliqueraient pas.
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
