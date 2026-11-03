package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/floopich/mire/internal/docsis"
)

// maxRunRows borne les lignes relues pour dater un constat : un mois de
// releves a une minute d'intervalle.
const maxRunRows = 45000

// DownstreamBack relit un canal descendant, du releve le plus recent au plus
// ancien, entre from et to, et passe chaque ligne a keep jusqu'a ce qu'elle
// renvoie false.
func DownstreamBack(ctx context.Context, db *sql.DB, channelID int, from, to time.Time, keep func(time.Time, docsis.Downstream) bool) error {
	rows, err := db.QueryContext(ctx,
		`SELECT ts, frequency_mhz, modulation, power_dbmv, snr_db FROM ds_channels
		 WHERE channel_id = ? AND ts BETWEEN ? AND ? ORDER BY ts DESC LIMIT ?`,
		channelID, from.UTC().Unix(), to.UTC().Unix(), maxRunRows)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var ts int64
		c := docsis.Downstream{ChannelID: channelID}
		if err := rows.Scan(&ts, &c.FrequencyMHz, &c.Modulation, &c.PowerDBmV, &c.SNRDB); err != nil {
			return err
		}
		if !keep(time.Unix(ts, 0).UTC(), c) {
			return nil
		}
	}
	return rows.Err()
}

// UpstreamBack relit un canal montant de la meme facon.
func UpstreamBack(ctx context.Context, db *sql.DB, channelID int, from, to time.Time, keep func(time.Time, docsis.Upstream) bool) error {
	rows, err := db.QueryContext(ctx,
		`SELECT ts, frequency_mhz, modulation, multiplex, power_dbmv FROM us_channels
		 WHERE channel_id = ? AND ts BETWEEN ? AND ? ORDER BY ts DESC LIMIT ?`,
		channelID, from.UTC().Unix(), to.UTC().Unix(), maxRunRows)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var ts int64
		c := docsis.Upstream{ChannelID: channelID}
		if err := rows.Scan(&ts, &c.FrequencyMHz, &c.Modulation, &c.Multiplex, &c.PowerDBmV); err != nil {
			return err
		}
		if !keep(time.Unix(ts, 0).UTC(), c) {
			return nil
		}
	}
	return rows.Err()
}
