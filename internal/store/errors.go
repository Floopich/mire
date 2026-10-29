package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/floopich/mire/internal/docsis"
)

// ErrorCount compte les erreurs d'un canal descendant sur une periode.
type ErrorCount struct {
	Corrected   int64
	Uncorrected int64
}

// Total renvoie le nombre d'erreurs, corrigees ou non.
func (e ErrorCount) Total() int64 { return e.Corrected + e.Uncorrected }

// ErrorPoint est l'accroissement des compteurs d'un canal entre deux releves.
type ErrorPoint struct {
	Timestamp   time.Time
	Corrected   int64
	Uncorrected int64
}

// ErrorTotals compte, par canal descendant, les erreurs survenues entre from
// et to.
//
// Les compteurs du modem cumulent depuis son demarrage : on somme leurs
// accroissements d'un releve au suivant, en partant du dernier releve
// anterieur a from pour ne rien perdre de l'intervalle qui le chevauche.
func ErrorTotals(ctx context.Context, db *sql.DB, from, to time.Time) (map[int]ErrorCount, error) {
	var lo sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT max(ts) FROM readings WHERE ts < ?`, from.Unix()).Scan(&lo); err != nil {
		return nil, err
	}
	start := from.Unix()
	if lo.Valid {
		start = lo.Int64
	}
	// Le filtre passe par readings (index sur ts) puis ds_channels (index sur
	// reading_id) : aucune lecture de la table entiere, meme apres des annees.
	rows, err := db.QueryContext(ctx,
		`SELECT channel_id, corr_errors, noncorr_errors FROM ds_channels
		 WHERE reading_id IN (SELECT id FROM readings WHERE ts >= ? AND ts <= ?)
		 ORDER BY channel_id, ts, reading_id`, start, to.Unix())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[int]ErrorCount{}
	type counters struct{ corr, noncorr int64 }
	prev := map[int]counters{}
	for rows.Next() {
		var id int
		var c counters
		if err := rows.Scan(&id, &c.corr, &c.noncorr); err != nil {
			return nil, err
		}
		// Un canal vu une seule fois n'a pas d'accroissement : il reste absent
		// du resultat, inconnu plutot que compte sans erreur.
		if p, ok := prev[id]; ok {
			e := out[id]
			e.Corrected += docsis.CounterDelta(p.corr, c.corr)
			e.Uncorrected += docsis.CounterDelta(p.noncorr, c.noncorr)
			out[id] = e
		}
		prev[id] = c
	}
	return out, rows.Err()
}

// ErrorHistory renvoie l'accroissement des compteurs d'un canal descendant a
// chaque releve entre from et to.
func ErrorHistory(ctx context.Context, db *sql.DB, channelID int, from, to time.Time) ([]ErrorPoint, error) {
	var lo sql.NullInt64
	if err := db.QueryRowContext(ctx,
		`SELECT max(ts) FROM ds_channels WHERE channel_id = ? AND ts < ?`, channelID, from.Unix()).Scan(&lo); err != nil {
		return nil, err
	}
	start := from.Unix()
	if lo.Valid {
		start = lo.Int64
	}
	rows, err := db.QueryContext(ctx,
		`SELECT ts, corr_errors, noncorr_errors FROM ds_channels
		 WHERE channel_id = ? AND ts >= ? AND ts <= ? ORDER BY ts, reading_id`, channelID, start, to.Unix())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []ErrorPoint
	var prevCorr, prevNoncorr int64
	first := true
	for rows.Next() {
		var ts, corr, noncorr int64
		if err := rows.Scan(&ts, &corr, &noncorr); err != nil {
			return nil, err
		}
		if !first {
			out = append(out, ErrorPoint{
				Timestamp:   time.Unix(ts, 0).UTC(),
				Corrected:   docsis.CounterDelta(prevCorr, corr),
				Uncorrected: docsis.CounterDelta(prevNoncorr, noncorr),
			})
		}
		first = false
		prevCorr, prevNoncorr = corr, noncorr
	}
	return out, rows.Err()
}
