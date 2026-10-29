package store

import (
	"context"
	"database/sql"
	"time"
)

// DayLine resume une journee de releves : moyennes des canaux SC-QAM.
//
// Les canaux OFDM et OFDMA sont ecartes : leur arrivee ou leur depart, ou une
// mesure douteuse du modem, deplaceraient la moyenne sans que la ligne ait
// change.
type DayLine struct {
	Day      string // AAAA-MM-JJ, heure locale
	DSPower  *float64
	DSSNR    *float64
	USPower  *float64
	Readings int
}

// DailyLine renvoie les moyennes journalieres entre from et to.
func DailyLine(ctx context.Context, db *sql.DB, from, to time.Time) ([]DayLine, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT d.day, d.power, d.snr, u.power, d.n FROM (
			SELECT date(r.ts, 'unixepoch', 'localtime') AS day,
			       avg(c.power_dbmv) AS power, avg(c.snr_db) AS snr, count(DISTINCT r.id) AS n
			FROM readings r JOIN ds_channels c ON c.reading_id = r.id
			WHERE r.ts >= ? AND r.ts < ? AND upper(c.modulation) != 'OFDM'
			GROUP BY day) d
		LEFT JOIN (
			SELECT date(r.ts, 'unixepoch', 'localtime') AS day, avg(c.power_dbmv) AS power
			FROM readings r JOIN us_channels c ON c.reading_id = r.id
			WHERE r.ts >= ? AND r.ts < ? AND upper(c.modulation) NOT LIKE '%OFDMA%'
			GROUP BY day) u ON u.day = d.day
		ORDER BY d.day`, from.Unix(), to.Unix(), from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []DayLine
	for rows.Next() {
		var d DayLine
		if err := rows.Scan(&d.Day, &d.DSPower, &d.DSSNR, &d.USPower, &d.Readings); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// WeatherDay est la temperature d'une journee, en degres Celsius.
type WeatherDay struct {
	Day            string // AAAA-MM-JJ
	Min, Max, Mean *float64
}

// SaveWeather enregistre ou remplace des journees de meteo.
func SaveWeather(ctx context.Context, db *sql.DB, days []WeatherDay) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO weather_daily(day, tmin, tmax, tmean) VALUES (?, ?, ?, ?)
		ON CONFLICT(day) DO UPDATE SET tmin = excluded.tmin, tmax = excluded.tmax, tmean = excluded.tmean`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	for _, d := range days {
		if _, err := stmt.ExecContext(ctx, d.Day, d.Min, d.Max, d.Mean); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Weather renvoie les journees de meteo de from a to inclus (AAAA-MM-JJ).
func Weather(ctx context.Context, db *sql.DB, from, to string) ([]WeatherDay, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT day, tmin, tmax, tmean FROM weather_daily WHERE day >= ? AND day <= ? ORDER BY day`, from, to)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []WeatherDay
	for rows.Next() {
		var d WeatherDay
		if err := rows.Scan(&d.Day, &d.Min, &d.Max, &d.Mean); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
