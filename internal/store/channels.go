package store

import (
	"context"
	"database/sql"
	"time"
)

// ChannelSummary resume l'etat d'un canal au dernier releve.
type ChannelSummary struct {
	ChannelID  int
	Modulation string
	Power      *float64
	SNR        *float64
	LastSeen   time.Time
}

// DownstreamChannels liste les canaux descendants vus sur la periode, a
// partir du dernier releve de chaque canal.
func DownstreamChannels(ctx context.Context, db *sql.DB, since time.Time) ([]ChannelSummary, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT c.channel_id, c.modulation, c.power_dbmv, c.snr_db, c.ts
		FROM ds_channels c
		JOIN (SELECT channel_id, max(ts) AS ts FROM ds_channels
		      WHERE ts >= ? GROUP BY channel_id) d
		  ON c.channel_id = d.channel_id AND c.ts = d.ts
		ORDER BY c.channel_id`, since.UTC().Unix())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []ChannelSummary
	for rows.Next() {
		var s ChannelSummary
		var ts int64
		if err := rows.Scan(&s.ChannelID, &s.Modulation, &s.Power, &s.SNR, &ts); err != nil {
			return nil, err
		}
		s.LastSeen = time.Unix(ts, 0).UTC()
		out = append(out, s)
	}
	return out, rows.Err()
}

// ReadingCount compte les releves enregistres.
func ReadingCount(ctx context.Context, db *sql.DB) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM readings`).Scan(&n)
	return n, err
}

// UpstreamChannels liste les canaux montants vus sur la periode.
func UpstreamChannels(ctx context.Context, db *sql.DB, since time.Time) ([]ChannelSummary, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT c.channel_id, c.modulation, c.power_dbmv, c.ts
		FROM us_channels c
		JOIN (SELECT channel_id, max(ts) AS ts FROM us_channels
		      WHERE ts >= ? GROUP BY channel_id) d
		  ON c.channel_id = d.channel_id AND c.ts = d.ts
		ORDER BY c.channel_id`, since.UTC().Unix())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []ChannelSummary
	for rows.Next() {
		var s ChannelSummary
		var ts int64
		if err := rows.Scan(&s.ChannelID, &s.Modulation, &s.Power, &ts); err != nil {
			return nil, err
		}
		s.LastSeen = time.Unix(ts, 0).UTC()
		out = append(out, s)
	}
	return out, rows.Err()
}

// DownstreamChannel resume le dernier etat connu d'un canal descendant ; la
// modulation choisit les seuils a tracer.
func DownstreamChannel(ctx context.Context, db *sql.DB, channelID int) (ChannelSummary, error) {
	s := ChannelSummary{ChannelID: channelID}
	var ts int64
	err := db.QueryRowContext(ctx, `
		SELECT modulation, power_dbmv, snr_db, ts FROM ds_channels
		WHERE channel_id = ? ORDER BY ts DESC LIMIT 1`, channelID).
		Scan(&s.Modulation, &s.Power, &s.SNR, &ts)
	if err != nil {
		return s, err
	}
	s.LastSeen = time.Unix(ts, 0).UTC()
	return s, nil
}

// UpstreamChannel resume le dernier etat connu d'un canal montant.
func UpstreamChannel(ctx context.Context, db *sql.DB, channelID int) (ChannelSummary, error) {
	s := ChannelSummary{ChannelID: channelID}
	var ts int64
	err := db.QueryRowContext(ctx, `
		SELECT modulation, power_dbmv, ts FROM us_channels
		WHERE channel_id = ? ORDER BY ts DESC LIMIT 1`, channelID).
		Scan(&s.Modulation, &s.Power, &ts)
	if err != nil {
		return s, err
	}
	s.LastSeen = time.Unix(ts, 0).UTC()
	return s, nil
}

// LastReadingTime renvoie l'heure du dernier releve, zero si la base est vide.
func LastReadingTime(ctx context.Context, db *sql.DB) (time.Time, error) {
	var ts sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT max(ts) FROM readings`).Scan(&ts); err != nil {
		return time.Time{}, err
	}
	if !ts.Valid {
		return time.Time{}, nil
	}
	return time.Unix(ts.Int64, 0).UTC(), nil
}

// FirstReadingTime renvoie l'heure du premier releve conserve.
func FirstReadingTime(ctx context.Context, db *sql.DB) (time.Time, error) {
	var ts sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT min(ts) FROM readings`).Scan(&ts); err != nil {
		return time.Time{}, err
	}
	if !ts.Valid {
		return time.Time{}, nil
	}
	return time.Unix(ts.Int64, 0).UTC(), nil
}

// ModulationShare est la part des releves faits a une modulation.
type ModulationShare struct {
	Modulation string
	Readings   int
}

// OFDMAModulations compte, depuis from, les modulations relevees sur les
// canaux montants OFDMA, de la plus frequente a la plus rare.
func OFDMAModulations(ctx context.Context, db *sql.DB, from time.Time) ([]ModulationShare, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT upper(multiplex), count(*) FROM us_channels
		WHERE ts >= ? AND upper(modulation) LIKE '%OFDMA%' AND multiplex != ''
		GROUP BY upper(multiplex) ORDER BY count(*) DESC`, from.Unix())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ModulationShare
	for rows.Next() {
		var m ModulationShare
		if err := rows.Scan(&m.Modulation, &m.Readings); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
