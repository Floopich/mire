package store

import (
	"context"
	"database/sql"
	"strconv"
	"time"

	"github.com/floopich/mire/internal/docsis"
)

// Les donnees d'une periode s'exportent en tableaux bruts, un par sujet,
// pour les analyser dans un tableur ou un autre outil. Les lignes sont lues
// et ecrites au fil de l'eau : un an de releves ne passe pas en memoire.
//
// Format : horodatage ISO 8601 avec le decalage horaire local, nombres a
// point decimal, cellule vide pour une valeur absente.

// Export est un tableau exportable.
type Export struct {
	// Name nomme le fichier et l'adresse : minuscules, sans espace.
	Name string
	// Label le decrit dans l'interface.
	Label  string
	Header []string
	rows   func(ctx context.Context, db *sql.DB, from, to time.Time, write func([]string) error) error
}

// Write ecrit l'en-tete puis les lignes de la periode [from, to).
func (e Export) Write(ctx context.Context, db *sql.DB, from, to time.Time, write func([]string) error) error {
	if err := write(e.Header); err != nil {
		return err
	}
	return e.rows(ctx, db, from, to, write)
}

// Exports renvoie les tableaux, dans l'ordre de l'interface.
func Exports() []Export {
	return []Export{
		{Name: "descendant", Label: "Canaux descendants",
			Header: []string{"horodatage", "canal", "frequence_mhz", "modulation", "puissance_dbmv", "snr_db",
				"erreurs_corrigees", "erreurs_non_corrigees"},
			rows: exportDownstream},
		{Name: "montant", Label: "Canaux montants",
			Header: []string{"horodatage", "canal", "frequence_mhz", "modulation", "multiplexage", "puissance_dbmv"},
			rows:   exportUpstream},
		{Name: "internet", Label: "Sonde Internet",
			Header: []string{"debut_tranche", "tentatives", "echecs", "latence_min_ms", "latence_moy_ms", "latence_max_ms"},
			rows:   exportProbe},
		{Name: "coupures", Label: "Coupures",
			Header: []string{"debut", "fin", "duree_s", "cause", "detail"},
			rows:   exportOutages},
		{Name: "debit", Label: "Débit",
			Header: []string{"horodatage", "descendant_mbps", "montant_mbps", "latence_ms", "gigue_ms", "pertes_pct", "serveur"},
			rows:   exportSpeed},
		{Name: "journal", Label: "Journal",
			Header: []string{"horodatage", "type", "niveau", "sens", "canal", "message"},
			rows:   exportEvents},
	}
}

// ExportByName renvoie le tableau demande.
func ExportByName(name string) (Export, bool) {
	for _, e := range Exports() {
		if e.Name == name {
			return e, true
		}
	}
	return Export{}, false
}

func stamp(ts int64) string {
	return time.Unix(ts, 0).In(time.Local).Format(time.RFC3339)
}

func num(v sql.NullFloat64) string {
	if !v.Valid {
		return ""
	}
	return strconv.FormatFloat(v.Float64, 'f', -1, 64)
}

func exportDownstream(ctx context.Context, db *sql.DB, from, to time.Time, write func([]string) error) error {
	// Le filtre passe par readings (index sur ts) puis ds_channels (index sur
	// reading_id), comme ErrorTotals.
	rows, err := db.QueryContext(ctx,
		`SELECT ts, channel_id, frequency_mhz, modulation, power_dbmv, snr_db, corr_errors, noncorr_errors
		 FROM ds_channels WHERE reading_id IN (SELECT id FROM readings WHERE ts >= ? AND ts < ?)
		 ORDER BY reading_id, channel_id`, from.Unix(), to.Unix())
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	// Les compteurs du modem cumulent depuis son demarrage : le tableau donne
	// les erreurs survenues depuis le releve precedent du meme canal.
	type counters struct{ corr, noncorr int64 }
	prev := map[int]counters{}
	for rows.Next() {
		var ts int64
		var id int
		var freq, power, snr sql.NullFloat64
		var mod string
		var c counters
		if err := rows.Scan(&ts, &id, &freq, &mod, &power, &snr, &c.corr, &c.noncorr); err != nil {
			return err
		}
		corr, noncorr := "", ""
		if p, ok := prev[id]; ok {
			corr = strconv.FormatInt(docsis.CounterDelta(p.corr, c.corr), 10)
			noncorr = strconv.FormatInt(docsis.CounterDelta(p.noncorr, c.noncorr), 10)
		}
		prev[id] = c
		if err := write([]string{stamp(ts), strconv.Itoa(id), num(freq), mod, num(power), num(snr), corr, noncorr}); err != nil {
			return err
		}
	}
	return rows.Err()
}

func exportUpstream(ctx context.Context, db *sql.DB, from, to time.Time, write func([]string) error) error {
	rows, err := db.QueryContext(ctx,
		`SELECT ts, channel_id, frequency_mhz, modulation, multiplex, power_dbmv
		 FROM us_channels WHERE reading_id IN (SELECT id FROM readings WHERE ts >= ? AND ts < ?)
		 ORDER BY reading_id, channel_id`, from.Unix(), to.Unix())
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var ts int64
		var id int
		var freq, power sql.NullFloat64
		var mod, mux string
		if err := rows.Scan(&ts, &id, &freq, &mod, &mux, &power); err != nil {
			return err
		}
		if err := write([]string{stamp(ts), strconv.Itoa(id), num(freq), mod, mux, num(power)}); err != nil {
			return err
		}
	}
	return rows.Err()
}

func exportProbe(ctx context.Context, db *sql.DB, from, to time.Time, write func([]string) error) error {
	rows, err := db.QueryContext(ctx,
		`SELECT ts, rounds, failed, rtt_min, rtt_avg, rtt_max FROM probe_samples
		 WHERE ts >= ? AND ts < ? ORDER BY ts`, from.Unix(), to.Unix())
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var ts int64
		var rounds, failed int
		var lo, avg, hi sql.NullFloat64
		if err := rows.Scan(&ts, &rounds, &failed, &lo, &avg, &hi); err != nil {
			return err
		}
		if err := write([]string{stamp(ts), strconv.Itoa(rounds), strconv.Itoa(failed), num(lo), num(avg), num(hi)}); err != nil {
			return err
		}
	}
	return rows.Err()
}

func exportOutages(ctx context.Context, db *sql.DB, from, to time.Time, write func([]string) error) error {
	outages, err := Outages(ctx, db, from, to)
	if err != nil {
		return err
	}
	// Du plus ancien au plus recent, comme les autres tableaux.
	for i := len(outages) - 1; i >= 0; i-- {
		o := outages[i]
		end, length := "", ""
		if !o.Ongoing() {
			end = stamp(o.End.Unix())
			length = strconv.FormatInt(int64(o.End.Sub(o.Start)/time.Second), 10)
		}
		if err := write([]string{stamp(o.Start.Unix()), end, length, o.Kind, o.Detail}); err != nil {
			return err
		}
	}
	return nil
}

func exportSpeed(ctx context.Context, db *sql.DB, from, to time.Time, write func([]string) error) error {
	tests, err := SpeedTests(ctx, db, from, to)
	if err != nil {
		return err
	}
	for _, s := range tests {
		f := func(v *float64) string {
			if v == nil {
				return ""
			}
			return strconv.FormatFloat(*v, 'f', -1, 64)
		}
		down, up := s.Download, s.Upload
		if err := write([]string{stamp(s.Time.Unix()), f(&down), f(&up), f(s.Ping), f(s.Jitter), f(s.Loss), s.Server}); err != nil {
			return err
		}
	}
	return nil
}

// levelNames reprend l'echelle des niveaux du journal.
var levelNames = map[int]string{1: "bon", 2: "tolere", 3: "a_surveiller", 4: "critique"}

func exportEvents(ctx context.Context, db *sql.DB, from, to time.Time, write func([]string) error) error {
	evs, err := EventsBetween(ctx, db, from, to)
	if err != nil {
		return err
	}
	for _, e := range evs {
		channel := ""
		if e.ChannelID > 0 {
			channel = strconv.Itoa(e.ChannelID)
		}
		if err := write([]string{stamp(e.Time.Unix()), e.Kind, levelNames[e.Level], e.Direction, channel, e.Message}); err != nil {
			return err
		}
	}
	return nil
}
