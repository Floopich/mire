package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ProbeSample agrege les tentatives de la sonde sur une tranche de temps.
type ProbeSample struct {
	Start  time.Time
	Rounds int
	Failed int
	// Latences en millisecondes des tentatives reussies ; nil sans reussite.
	RTTMin, RTTAvg, RTTMax *float64
}

// SaveProbeSample enregistre une tranche, ou la complete si elle existe
// deja : apres un redemarrage du service au milieu d'une tranche, les deux
// moities se cumulent au lieu de s'ecraser.
func SaveProbeSample(ctx context.Context, db *sql.DB, s ProbeSample) error {
	// Dans un UPDATE, les noms de colonnes designent l'ancienne ligne : la
	// moyenne se pondere par le nombre de reussites de chaque moitie.
	_, err := db.ExecContext(ctx, `
		INSERT INTO probe_samples(ts, rounds, failed, rtt_min, rtt_avg, rtt_max) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(ts) DO UPDATE SET
			rounds  = rounds + excluded.rounds,
			failed  = failed + excluded.failed,
			rtt_min = min(coalesce(rtt_min, excluded.rtt_min), coalesce(excluded.rtt_min, rtt_min)),
			rtt_max = max(coalesce(rtt_max, excluded.rtt_max), coalesce(excluded.rtt_max, rtt_max)),
			rtt_avg = CASE
				WHEN (rounds - failed) + (excluded.rounds - excluded.failed) = 0 THEN NULL
				ELSE (coalesce(rtt_avg, 0) * (rounds - failed) + coalesce(excluded.rtt_avg, 0) * (excluded.rounds - excluded.failed))
					/ ((rounds - failed) + (excluded.rounds - excluded.failed))
			END`,
		s.Start.Unix(), s.Rounds, s.Failed, s.RTTMin, s.RTTAvg, s.RTTMax)
	return err
}

// SaveProbeSamples enregistre des tranches nouvelles d'un coup, en une
// transaction : l'historique du mode demonstration.
func SaveProbeSamples(ctx context.Context, db *sql.DB, samples []ProbeSample) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx,
		`INSERT OR REPLACE INTO probe_samples(ts, rounds, failed, rtt_min, rtt_avg, rtt_max) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	for _, s := range samples {
		if _, err := stmt.ExecContext(ctx, s.Start.Unix(), s.Rounds, s.Failed, s.RTTMin, s.RTTAvg, s.RTTMax); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ProbeSamples renvoie les tranches commencees entre from et to.
func ProbeSamples(ctx context.Context, db *sql.DB, from, to time.Time) ([]ProbeSample, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT ts, rounds, failed, rtt_min, rtt_avg, rtt_max FROM probe_samples
		 WHERE ts >= ? AND ts <= ? ORDER BY ts`, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ProbeSample
	for rows.Next() {
		var s ProbeSample
		var ts int64
		if err := rows.Scan(&ts, &s.Rounds, &s.Failed, &s.RTTMin, &s.RTTAvg, &s.RTTMax); err != nil {
			return nil, err
		}
		s.Start = time.Unix(ts, 0).UTC()
		out = append(out, s)
	}
	return out, rows.Err()
}

// LastProbeSample renvoie le debut de la derniere tranche, zero s'il n'y en a pas.
func LastProbeSample(ctx context.Context, db *sql.DB) (time.Time, error) {
	var ts sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT max(ts) FROM probe_samples`).Scan(&ts); err != nil || !ts.Valid {
		return time.Time{}, err
	}
	return time.Unix(ts.Int64, 0).UTC(), nil
}

// LatestProbeSample renvoie la derniere tranche de la sonde, eventuellement
// en cours ; ok est faux s'il n'y en a aucune.
func LatestProbeSample(ctx context.Context, db *sql.DB) (s ProbeSample, ok bool, err error) {
	var ts int64
	err = db.QueryRowContext(ctx,
		`SELECT ts, rounds, failed, rtt_min, rtt_avg, rtt_max FROM probe_samples ORDER BY ts DESC LIMIT 1`).
		Scan(&ts, &s.Rounds, &s.Failed, &s.RTTMin, &s.RTTAvg, &s.RTTMax)
	if errors.Is(err, sql.ErrNoRows) {
		return ProbeSample{}, false, nil
	}
	if err != nil {
		return ProbeSample{}, false, err
	}
	s.Start = time.Unix(ts, 0).UTC()
	return s, true, nil
}

// Outage est une coupure constatee par la sonde.
type Outage struct {
	ID    int64
	Start time.Time
	// End est zero tant que la coupure dure.
	End    time.Time
	Kind   string
	Detail string
}

// Ongoing dit si la coupure dure encore.
func (o Outage) Ongoing() bool { return o.End.IsZero() }

// Duration renvoie la duree de la coupure, jusqu'a now si elle dure encore.
func (o Outage) Duration(now time.Time) time.Duration {
	if o.Ongoing() {
		return now.Sub(o.Start)
	}
	return o.End.Sub(o.Start)
}

// OpenOutage inscrit le debut d'une coupure.
func OpenOutage(ctx context.Context, db *sql.DB, kind string, start time.Time, detail string) (int64, error) {
	res, err := db.ExecContext(ctx,
		`INSERT INTO link_events(started_at, kind, detail) VALUES (?, ?, ?)`, start.Unix(), kind, detail)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// CloseOutage inscrit la fin d'une coupure.
func CloseOutage(ctx context.Context, db *sql.DB, id int64, end time.Time) error {
	_, err := db.ExecContext(ctx, `UPDATE link_events SET ended_at = ? WHERE id = ?`, end.Unix(), id)
	return err
}

// CurrentOutage renvoie la coupure en cours, s'il y en a une.
func CurrentOutage(ctx context.Context, db *sql.DB) (Outage, bool, error) {
	o, err := scanOutage(db.QueryRowContext(ctx,
		`SELECT id, started_at, ended_at, kind, detail FROM link_events
		 WHERE ended_at IS NULL ORDER BY started_at DESC LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return Outage{}, false, nil
	}
	return o, err == nil, err
}

// Outages renvoie les coupures qui touchent la fenetre, les plus recentes
// d'abord.
func Outages(ctx context.Context, db *sql.DB, from, to time.Time) ([]Outage, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, started_at, ended_at, kind, detail FROM link_events
		 WHERE started_at <= ? AND (ended_at IS NULL OR ended_at >= ?)
		 ORDER BY started_at DESC`, to.Unix(), from.Unix())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Outage
	for rows.Next() {
		o, err := scanOutage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

type scanner interface{ Scan(...any) error }

func scanOutage(row scanner) (Outage, error) {
	var o Outage
	var start int64
	var end sql.NullInt64
	if err := row.Scan(&o.ID, &start, &end, &o.Kind, &o.Detail); err != nil {
		return o, err
	}
	o.Start = time.Unix(start, 0).UTC()
	if end.Valid {
		o.End = time.Unix(end.Int64, 0).UTC()
	}
	return o, nil
}
