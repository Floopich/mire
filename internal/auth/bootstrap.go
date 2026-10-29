package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/floopich/mire/internal/store"
)

// KeyPasswordHash est la cle du reglage portant l'empreinte du mot de passe.
const KeyPasswordHash = "admin_password_hash"

// MinPasswordLength est la longueur minimale d'un mot de passe d'interface.
const MinPasswordLength = 8

// HasPassword dit si un mot de passe d'interface a ete choisi.
//
// Tant qu'il n'y en a pas, l'interface ne propose que l'assistant de premier
// demarrage, dont la premiere etape consiste justement a le choisir.
func HasPassword(ctx context.Context, db *sql.DB) (bool, error) {
	hash, err := store.Setting(ctx, db, KeyPasswordHash, "")
	return hash != "", err
}

// ErrPasswordExists signale que le premier mot de passe a deja ete choisi.
var ErrPasswordExists = errors.New("le mot de passe de l'interface est déjà choisi")

// SetInitialPassword choisit le premier mot de passe, depuis l'assistant.
//
// L'ecriture est atomique : deux visiteurs simultanes d'une instance neuve ne
// peuvent pas choisir chacun le leur ; le second recoit ErrPasswordExists.
func SetInitialPassword(ctx context.Context, db *sql.DB, password string) error {
	hash, err := hashChecked(password)
	if err != nil {
		return err
	}
	added, err := store.AddSetting(ctx, db, KeyPasswordHash, hash)
	if err != nil {
		return err
	}
	if !added {
		return ErrPasswordExists
	}
	return nil
}

func hashChecked(password string) (string, error) {
	if len(strings.TrimSpace(password)) < MinPasswordLength {
		return "", fmt.Errorf("mot de passe trop court : %d caractères au minimum", MinPasswordLength)
	}
	return HashPassword(password)
}

// SetPassword remplace le mot de passe et ferme toutes les sessions.
func SetPassword(ctx context.Context, db *sql.DB, password string) error {
	hash, err := hashChecked(password)
	if err != nil {
		return err
	}
	if err := store.SetSetting(ctx, db, KeyPasswordHash, hash); err != nil {
		return err
	}
	return NewSessions(db).RevokeAll(ctx)
}

// Check verifie un mot de passe contre l'empreinte enregistree.
func Check(ctx context.Context, db *sql.DB, password string) (bool, error) {
	hash, err := store.Setting(ctx, db, KeyPasswordHash, "")
	if err != nil || hash == "" {
		return false, err
	}
	return VerifyPassword(password, hash)
}
