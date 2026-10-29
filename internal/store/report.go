package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ChannelStats resume un canal sur une periode.
type ChannelStats struct {
	ChannelID  int
	Modulation string
	Frequency  *float64
	PowerMin   *float64
	PowerAvg   *float64
	PowerMax   *float64
	SNRMin     *float64
	SNRAvg     *float64
	Readings   int
	// OutOfBand compte les releves dont la puissance sort de la plage donnee.
	OutOfBand int
}

// DownstreamStats resume chaque canal descendant entre from et to. band est
// la plage nominale de puissance ; nil ne compte aucun depassement.
func DownstreamStats(ctx context.Context, db *sql.DB, from, to time.Time, band *[2]float64) ([]ChannelStats, error) {
	return channelStats(ctx, db, "ds_channels", "c.snr_db", from, to, band)
}

// UpstreamStats resume chaque canal montant entre from et to.
func UpstreamStats(ctx context.Context, db *sql.DB, from, to time.Time, band *[2]float64) ([]ChannelStats, error) {
	return channelStats(ctx, db, "us_channels", "NULL", from, to, band)
}

func channelStats(ctx context.Context, db *sql.DB, table, snr string, from, to time.Time, band *[2]float64) ([]ChannelStats, error) {
	lo, hi := -1e9, 1e9
	if band != nil {
		lo, hi = band[0], band[1]
	}
	// table et snr sont des constantes de ce paquet, jamais une saisie.
	rows, err := db.QueryContext(ctx, `
		SELECT c.channel_id, max(c.modulation), avg(c.frequency_mhz),
		       min(c.power_dbmv), avg(c.power_dbmv), max(c.power_dbmv),
		       min(`+snr+`), avg(`+snr+`), count(*),
		       sum(CASE WHEN c.power_dbmv < ? OR c.power_dbmv > ? THEN 1 ELSE 0 END)
		FROM readings r JOIN `+table+` c ON c.reading_id = r.id
		WHERE r.ts >= ? AND r.ts < ?
		GROUP BY c.channel_id ORDER BY c.channel_id`, lo, hi, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ChannelStats
	for rows.Next() {
		var c ChannelStats
		if err := rows.Scan(&c.ChannelID, &c.Modulation, &c.Frequency, &c.PowerMin, &c.PowerAvg, &c.PowerMax,
			&c.SNRMin, &c.SNRAvg, &c.Readings, &c.OutOfBand); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// PeriodLine resume une periode sur les canaux SC-QAM, comme la tendance.
type PeriodLine struct {
	DSPowerAvg, DSSNRAvg, DSSNRMin, USPowerAvg *float64
	Readings                                   int
}

// LineOver renvoie le resume de la ligne entre from et to.
func LineOver(ctx context.Context, db *sql.DB, from, to time.Time) (PeriodLine, error) {
	var p PeriodLine
	err := db.QueryRowContext(ctx, `
		SELECT avg(c.power_dbmv), avg(c.snr_db), min(c.snr_db), count(DISTINCT r.id)
		FROM readings r JOIN ds_channels c ON c.reading_id = r.id
		WHERE r.ts >= ? AND r.ts < ? AND upper(c.modulation) != 'OFDM'`, from.Unix(), to.Unix()).
		Scan(&p.DSPowerAvg, &p.DSSNRAvg, &p.DSSNRMin, &p.Readings)
	if err != nil {
		return PeriodLine{}, err
	}
	err = db.QueryRowContext(ctx, `
		SELECT avg(c.power_dbmv) FROM readings r JOIN us_channels c ON c.reading_id = r.id
		WHERE r.ts >= ? AND r.ts < ? AND upper(c.modulation) NOT LIKE '%OFDMA%'`, from.Unix(), to.Unix()).
		Scan(&p.USPowerAvg)
	return p, err
}

// EventsBetween lit les evenements de from a to, du plus ancien au plus
// recent ; sans type, tous.
func EventsBetween(ctx context.Context, db *sql.DB, from, to time.Time, kinds ...string) ([]Event, error) {
	query := `SELECT id, ts, kind, level, direction, channel_id, message FROM events WHERE ts >= ? AND ts < ?`
	args := []any{from.Unix(), to.Unix()}
	if len(kinds) > 0 {
		query += ` AND kind IN (?`
		args = append(args, kinds[0])
		for _, k := range kinds[1:] {
			query += `, ?`
			args = append(args, k)
		}
		query += `)`
	}
	rows, err := db.QueryContext(ctx, query+` ORDER BY ts, id`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Event
	for rows.Next() {
		var e Event
		var ts int64
		if err := rows.Scan(&e.ID, &ts, &e.Kind, &e.Level, &e.Direction, &e.ChannelID, &e.Message); err != nil {
			return nil, err
		}
		e.Time = time.Unix(ts, 0).UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}

// LastEventBefore renvoie le dernier evenement d'un type avant t.
func LastEventBefore(ctx context.Context, db *sql.DB, kind string, t time.Time) (Event, bool, error) {
	var e Event
	var ts int64
	err := db.QueryRowContext(ctx, `SELECT id, ts, kind, level, direction, channel_id, message FROM events
		WHERE kind = ? AND ts < ? ORDER BY ts DESC, id DESC LIMIT 1`, kind, t.Unix()).
		Scan(&e.ID, &ts, &e.Kind, &e.Level, &e.Direction, &e.ChannelID, &e.Message)
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, false, nil
	}
	if err != nil {
		return Event{}, false, err
	}
	e.Time = time.Unix(ts, 0).UTC()
	return e, true, nil
}
