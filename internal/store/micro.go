package store

import (
	"context"
	"database/sql"
	"time"
)

// MicroCut est une micro-coupure : Internet sans reponse entre Min et Max,
// a partir de Start.
type MicroCut struct {
	Start    time.Time
	Min, Max time.Duration
	Kind     string
}

// SaveMicroCut inscrit une micro-coupure ; la meme, relue, n'est pas
// dupliquee.
func SaveMicroCut(ctx context.Context, db *sql.DB, m MicroCut) error {
	_, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO micro_cuts(ts, min_s, max_s, kind) VALUES (?, ?, ?, ?)`,
		m.Start.Unix(), int64(m.Min/time.Second), int64(m.Max/time.Second), m.Kind)
	return err
}

// MicroCuts renvoie les micro-coupures commencees entre from et to, des plus
// anciennes aux plus recentes.
func MicroCuts(ctx context.Context, db *sql.DB, from, to time.Time) ([]MicroCut, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT ts, min_s, max_s, kind FROM micro_cuts WHERE ts >= ? AND ts < ? ORDER BY ts`, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []MicroCut
	for rows.Next() {
		var ts, lo, hi int64
		var m MicroCut
		if err := rows.Scan(&ts, &lo, &hi, &m.Kind); err != nil {
			return nil, err
		}
		m.Start = time.Unix(ts, 0).UTC()
		m.Min, m.Max = time.Duration(lo)*time.Second, time.Duration(hi)*time.Second
		out = append(out, m)
	}
	return out, rows.Err()
}

// MicroSince renvoie le debut de la detection des micro-coupures.
func MicroSince(ctx context.Context, db *sql.DB) (time.Time, error) {
	var ts int64
	if err := db.QueryRowContext(ctx, `SELECT min(ts) FROM micro_since`).Scan(&ts); err != nil {
		return time.Time{}, err
	}
	return time.Unix(ts, 0).UTC(), nil
}

// SetMicroSince avance ou recule le debut de la detection : l'historique de
// la demonstration la couvre en entier.
func SetMicroSince(ctx context.Context, db *sql.DB, t time.Time) error {
	_, err := db.ExecContext(ctx, `UPDATE micro_since SET ts = ?`, t.Unix())
	return err
}
