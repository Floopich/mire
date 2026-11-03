package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/floopich/mire/internal/docsis"
)

// La frise de correlation aligne sur une meme periode les mesures de toute
// la ligne : un point par releve, tous canaux confondus.

// LinePoint resume un releve : les extremes de ses canaux.
type LinePoint struct {
	Time time.Time
	// DSPowerMin, DSPowerMax : puissance descendante la plus basse et la plus
	// haute ; SNRMin : le SNR le plus bas ; USPowerMax : l'emission la plus
	// forte. nil quand aucun canal ne la donne.
	DSPowerMin, DSPowerMax, SNRMin, USPowerMax *float64
	// Uncorrected compte les erreurs non corrigees de toute la ligne depuis
	// le releve precedent ; Known est faux pour le premier releve de la
	// periode, sans point de comparaison.
	Uncorrected int64
	Known       bool
}

// LinePoints renvoie un point par releve entre from et to, du plus ancien au
// plus recent.
func LinePoints(ctx context.Context, db *sql.DB, from, to time.Time) ([]LinePoint, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT r.id, r.ts,
			(SELECT min(power_dbmv) FROM ds_channels WHERE reading_id = r.id),
			(SELECT max(power_dbmv) FROM ds_channels WHERE reading_id = r.id),
			(SELECT min(snr_db) FROM ds_channels WHERE reading_id = r.id),
			(SELECT max(power_dbmv) FROM us_channels WHERE reading_id = r.id)
		FROM readings r WHERE r.ts >= ? AND r.ts <= ? ORDER BY r.ts, r.id`, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []LinePoint
	ids := map[int64]int{}
	for rows.Next() {
		var id, ts int64
		var p LinePoint
		if err := rows.Scan(&id, &ts, &p.DSPowerMin, &p.DSPowerMax, &p.SNRMin, &p.USPowerMax); err != nil {
			return nil, err
		}
		p.Time = time.Unix(ts, 0).UTC()
		ids[id] = len(out)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil || len(out) == 0 {
		return out, err
	}

	// Erreurs : l'accroissement des compteurs de chaque canal d'un releve au
	// suivant, en partant du releve qui precede la periode. Un canal absent
	// du releve precedent ne compte pas : rien a comparer.
	erows, err := db.QueryContext(ctx, `
		SELECT reading_id, channel_id, corr_errors, noncorr_errors FROM ds_channels
		WHERE reading_id IN (SELECT id FROM readings WHERE ts >= coalesce(
			(SELECT max(ts) FROM readings WHERE ts < ?), ?) AND ts <= ?)
		ORDER BY ts, reading_id`, from.Unix(), from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer func() { _ = erows.Close() }()
	type counters struct{ corr, noncorr int64 }
	prev := map[int]counters{}
	prevReading := int64(-1)
	seenBefore := map[int]bool{}
	current := map[int]bool{}
	for erows.Next() {
		var reading int64
		var ch int
		var c counters
		if err := erows.Scan(&reading, &ch, &c.corr, &c.noncorr); err != nil {
			return nil, err
		}
		if reading != prevReading {
			seenBefore, current = current, map[int]bool{}
			prevReading = reading
		}
		current[ch] = true
		if i, ok := ids[reading]; ok && seenBefore[ch] {
			out[i].Uncorrected += docsis.CounterDelta(prev[ch].noncorr, c.noncorr)
			out[i].Known = true
		}
		prev[ch] = c
	}
	return out, erows.Err()
}

// StateBefore renvoie le niveau de la ligne inscrit au journal avant t ; ok
// est faux sans changement d'etat anterieur.
func StateBefore(ctx context.Context, db *sql.DB, kind string, t time.Time) (level int, ok bool, err error) {
	err = db.QueryRowContext(ctx,
		`SELECT level FROM events WHERE kind = ? AND ts < ? ORDER BY ts DESC, id DESC LIMIT 1`, kind, t.Unix()).Scan(&level)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	return level, err == nil, err
}
