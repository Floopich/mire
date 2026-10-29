package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

// SessionLifetime borne la duree d'une session.
const SessionLifetime = 30 * 24 * time.Hour

// ErrNoSession signale un jeton absent, expire ou inconnu.
var ErrNoSession = errors.New("session absente ou expiree")

// Sessions gere les sessions d'administration en base.
type Sessions struct {
	db  *sql.DB
	now func() time.Time
}

func NewSessions(db *sql.DB) *Sessions {
	return &Sessions{db: db, now: time.Now}
}

// Create ouvre une session et renvoie le jeton a placer dans le cookie.
func (s *Sessions) Create(ctx context.Context) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := s.now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions(token_hash, created_at, expires_at) VALUES (?, ?, ?)`,
		digest(token), now.Unix(), now.Add(SessionLifetime).Unix())
	if err != nil {
		return "", err
	}
	return token, nil
}

// Valid indique si un jeton designe une session active.
func (s *Sessions) Valid(ctx context.Context, token string) error {
	if token == "" {
		return ErrNoSession
	}
	var expires int64
	err := s.db.QueryRowContext(ctx,
		`SELECT expires_at FROM sessions WHERE token_hash = ?`, digest(token)).Scan(&expires)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoSession
	}
	if err != nil {
		return err
	}
	if s.now().UTC().Unix() >= expires {
		return ErrNoSession
	}
	return nil
}

// Revoke ferme une session.
func (s *Sessions) Revoke(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, digest(token))
	return err
}

// RevokeAll ferme toutes les sessions.
//
// A appeler apres un changement de mot de passe : sans cela, une session
// ouverte par quelqu'un qui connaissait l'ancien resterait valide trente jours.
func (s *Sessions) RevokeAll(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions`)
	return err
}

// Prune retire les sessions expirees.
func (s *Sessions) Prune(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at <= ?`, s.now().UTC().Unix())
	return err
}

func digest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
