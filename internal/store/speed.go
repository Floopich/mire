package store

import (
	"context"
	"database/sql"
	"time"
)

// SpeedTest est une mesure de debit.
type SpeedTest struct {
	Time time.Time
	// Debits en megabits par seconde.
	Download, Upload float64
	// Latence et gigue en millisecondes, pertes en % ; nil si inconnues.
	Ping, Jitter, Loss *float64
	Server             string
}

// SaveSpeedTests enregistre des mesures, en ignorant celles deja connues.
// Elle renvoie le nombre de mesures nouvelles.
func SaveSpeedTests(ctx context.Context, db *sql.DB, tests []SpeedTest) (int, error) {
	if len(tests) == 0 {
		return 0, nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx,
		`INSERT OR IGNORE INTO speed_tests(ts, download_mbps, upload_mbps, ping_ms, jitter_ms, loss_pct, server)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = stmt.Close() }()
	added := 0
	for _, t := range tests {
		res, err := stmt.ExecContext(ctx, t.Time.Unix(), t.Download, t.Upload, t.Ping, t.Jitter, t.Loss, t.Server)
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
		}
	}
	return added, tx.Commit()
}

// LastSpeedTestTime renvoie l'heure de la derniere mesure, zero sans mesure.
func LastSpeedTestTime(ctx context.Context, db *sql.DB) (time.Time, error) {
	var ts sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT max(ts) FROM speed_tests`).Scan(&ts); err != nil || !ts.Valid {
		return time.Time{}, err
	}
	return time.Unix(ts.Int64, 0).UTC(), nil
}

// SpeedTests lit les mesures de [from, to), de la plus ancienne a la plus
// recente.
func SpeedTests(ctx context.Context, db *sql.DB, from, to time.Time) ([]SpeedTest, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT ts, download_mbps, upload_mbps, ping_ms, jitter_ms, loss_pct, server FROM speed_tests
		 WHERE ts >= ? AND ts < ? ORDER BY ts`, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []SpeedTest
	for rows.Next() {
		var t SpeedTest
		var ts int64
		var ping, jitter, loss sql.NullFloat64
		if err := rows.Scan(&ts, &t.Download, &t.Upload, &ping, &jitter, &loss, &t.Server); err != nil {
			return nil, err
		}
		t.Time = time.Unix(ts, 0).UTC()
		t.Ping, t.Jitter, t.Loss = ptr(ping), ptr(jitter), ptr(loss)
		out = append(out, t)
	}
	return out, rows.Err()
}

func ptr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	return &v.Float64
}
