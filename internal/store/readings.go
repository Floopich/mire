package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/floopich/mire/internal/docsis"
)

// SaveSnapshot enregistre un releve et ses canaux.
//
// Tout passe dans une seule transaction : un releve dont la moitie des canaux
// manquerait serait pire qu'un releve absent, parce qu'il se lirait comme une
// ligne ou des canaux ont disparu.
func SaveSnapshot(ctx context.Context, db *sql.DB, snap docsis.Snapshot) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	ts := snap.Timestamp.UTC().Unix()
	res, err := tx.ExecContext(ctx,
		`INSERT INTO readings(ts, docsis) VALUES (?, ?)`, ts, snap.DOCSIS)
	if err != nil {
		return 0, fmt.Errorf("enregistrement du relevé : %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	ds, err := tx.PrepareContext(ctx, `INSERT INTO ds_channels
		(reading_id, ts, channel_id, frequency_mhz, modulation, power_dbmv, snr_db, corr_errors, noncorr_errors)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = ds.Close() }()
	for _, c := range snap.Downstream {
		if _, err := ds.ExecContext(ctx, id, ts, c.ChannelID, c.FrequencyMHz,
			c.Modulation, c.PowerDBmV, c.SNRDB, c.CorrErrors, c.NonCorrErrors); err != nil {
			return 0, fmt.Errorf("canal descendant %d: %w", c.ChannelID, err)
		}
	}

	us, err := tx.PrepareContext(ctx, `INSERT INTO us_channels
		(reading_id, ts, channel_id, frequency_mhz, modulation, multiplex, power_dbmv)
		VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = us.Close() }()
	for _, c := range snap.Upstream {
		if _, err := us.ExecContext(ctx, id, ts, c.ChannelID, c.FrequencyMHz,
			c.Modulation, c.Multiplex, c.PowerDBmV); err != nil {
			return 0, fmt.Errorf("canal montant %d: %w", c.ChannelID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// Point est une valeur horodatee d'un canal.
type Point struct {
	Timestamp time.Time
	Power     *float64
	SNR       *float64
}

// DownstreamHistory relit l'historique d'un canal descendant.
//
// C'est la requete que l'index (channel_id, ts) sert : elle doit rester
// utilisable sur dix ans de releves.
func DownstreamHistory(ctx context.Context, db *sql.DB, channelID int, from, to time.Time) ([]Point, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT ts, power_dbmv, snr_db FROM ds_channels
		 WHERE channel_id = ? AND ts BETWEEN ? AND ? ORDER BY ts`,
		channelID, from.UTC().Unix(), to.UTC().Unix())
	if err != nil {
		return nil, fmt.Errorf("historique du canal %d: %w", channelID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []Point
	for rows.Next() {
		var ts int64
		var p Point
		if err := rows.Scan(&ts, &p.Power, &p.SNR); err != nil {
			return nil, err
		}
		p.Timestamp = time.Unix(ts, 0).UTC()
		out = append(out, p)
	}
	return out, rows.Err()
}

// LatestSnapshot relit le releve le plus recent, ou sql.ErrNoRows.
func LatestSnapshot(ctx context.Context, db *sql.DB) (docsis.Snapshot, error) {
	return snapshotWhere(ctx, db, `SELECT id, ts, docsis FROM readings ORDER BY ts DESC, id DESC LIMIT 1`)
}

// SnapshotBefore relit le dernier releve strictement anterieur a t, ou
// sql.ErrNoRows : c'est la reference pour detecter ce qui a change.
func SnapshotBefore(ctx context.Context, db *sql.DB, t time.Time) (docsis.Snapshot, error) {
	return snapshotWhere(ctx, db,
		`SELECT id, ts, docsis FROM readings WHERE ts < ? ORDER BY ts DESC, id DESC LIMIT 1`, t.Unix())
}

func snapshotWhere(ctx context.Context, db *sql.DB, query string, args ...any) (docsis.Snapshot, error) {
	var id int64
	var ts int64
	var version string
	err := db.QueryRowContext(ctx, query, args...).Scan(&id, &ts, &version)
	if err == sql.ErrNoRows {
		return docsis.Snapshot{}, sql.ErrNoRows
	}
	if err != nil {
		return docsis.Snapshot{}, err
	}
	snap := docsis.Snapshot{Timestamp: time.Unix(ts, 0).UTC(), DOCSIS: version}

	dsRows, err := db.QueryContext(ctx,
		`SELECT channel_id, frequency_mhz, modulation, power_dbmv, snr_db, corr_errors, noncorr_errors
		 FROM ds_channels WHERE reading_id = ? ORDER BY channel_id`, id)
	if err != nil {
		return snap, err
	}
	defer func() { _ = dsRows.Close() }()
	for dsRows.Next() {
		var c docsis.Downstream
		if err := dsRows.Scan(&c.ChannelID, &c.FrequencyMHz, &c.Modulation,
			&c.PowerDBmV, &c.SNRDB, &c.CorrErrors, &c.NonCorrErrors); err != nil {
			return snap, err
		}
		snap.Downstream = append(snap.Downstream, c)
	}
	if err := dsRows.Err(); err != nil {
		return snap, err
	}

	usRows, err := db.QueryContext(ctx,
		`SELECT channel_id, frequency_mhz, modulation, multiplex, power_dbmv
		 FROM us_channels WHERE reading_id = ? ORDER BY channel_id`, id)
	if err != nil {
		return snap, err
	}
	defer func() { _ = usRows.Close() }()
	for usRows.Next() {
		var c docsis.Upstream
		if err := usRows.Scan(&c.ChannelID, &c.FrequencyMHz, &c.Modulation,
			&c.Multiplex, &c.PowerDBmV); err != nil {
			return snap, err
		}
		snap.Upstream = append(snap.Upstream, c)
	}
	return snap, usRows.Err()
}

// UpstreamHistory relit l'historique de puissance d'un canal montant.
//
// La voie montante n'a pas de SNR cote modem : seul Power est renseigne.
func UpstreamHistory(ctx context.Context, db *sql.DB, channelID int, from, to time.Time) ([]Point, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT ts, power_dbmv FROM us_channels
		 WHERE channel_id = ? AND ts BETWEEN ? AND ? ORDER BY ts`,
		channelID, from.UTC().Unix(), to.UTC().Unix())
	if err != nil {
		return nil, fmt.Errorf("historique du canal montant %d: %w", channelID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []Point
	for rows.Next() {
		var ts int64
		var p Point
		if err := rows.Scan(&ts, &p.Power); err != nil {
			return nil, err
		}
		p.Timestamp = time.Unix(ts, 0).UTC()
		out = append(out, p)
	}
	return out, rows.Err()
}
