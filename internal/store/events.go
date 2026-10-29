package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Event est une entree du journal de la ligne.
type Event struct {
	ID        int64
	Time      time.Time
	Kind      string
	Level     int // meme echelle que analyze.Level
	Direction string
	ChannelID int
	Message   string
}

// SaveEvents inscrit des evenements au journal, en une transaction.
func SaveEvents(ctx context.Context, db *sql.DB, events []Event) error {
	if len(events) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, e := range events {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO events(ts, kind, level, direction, channel_id, message) VALUES (?, ?, ?, ?, ?, ?)`,
			e.Time.Unix(), e.Kind, e.Level, e.Direction, e.ChannelID, e.Message); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// EventFilter restreint une lecture du journal.
type EventFilter struct {
	From, To time.Time
	// Direction et ChannelID limitent aux evenements d'un canal ; un
	// evenement portant sur plusieurs canaux (ChannelID 0) n'y figure pas.
	Direction string
	ChannelID int
	Limit     int
}

// Events lit le journal, du plus recent au plus ancien.
func Events(ctx context.Context, db *sql.DB, f EventFilter) ([]Event, error) {
	query := `SELECT id, ts, kind, level, direction, channel_id, message FROM events WHERE ts >= ? AND ts <= ?`
	args := []any{f.From.Unix(), f.To.Unix()}
	if f.ChannelID != 0 {
		query += ` AND direction = ? AND channel_id = ?`
		args = append(args, f.Direction, f.ChannelID)
	}
	query += ` ORDER BY ts DESC, id DESC`
	if f.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, f.Limit)
	}
	rows, err := db.QueryContext(ctx, query, args...)
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

// LastEvent renvoie le dernier evenement de l'un des types donnes, et false
// s'il n'y en a aucun.
func LastEvent(ctx context.Context, db *sql.DB, kinds ...string) (Event, bool, error) {
	if len(kinds) == 0 {
		return Event{}, false, nil
	}
	query := `SELECT id, ts, kind, level, direction, channel_id, message FROM events WHERE kind IN (?`
	args := []any{kinds[0]}
	for _, k := range kinds[1:] {
		query += `, ?`
		args = append(args, k)
	}
	query += `) ORDER BY ts DESC, id DESC LIMIT 1`
	var e Event
	var ts int64
	err := db.QueryRowContext(ctx, query, args...).
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

// EventsAfter lit les evenements inscrits apres l'identifiant donne, dans
// l'ordre ou ils l'ont ete.
func EventsAfter(ctx context.Context, db *sql.DB, afterID int64, limit int) ([]Event, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, ts, kind, level, direction, channel_id, message FROM events
		 WHERE id > ? ORDER BY id LIMIT ?`, afterID, limit)
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

// LastEventID renvoie l'identifiant du dernier evenement, 0 si le journal est vide.
func LastEventID(ctx context.Context, db *sql.DB) (int64, error) {
	var id sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT max(id) FROM events`).Scan(&id)
	return id.Int64, err
}

// NoteKind est le type des notes ecrites par l'utilisateur dans le journal :
// « technicien passe », « connecteur remplace ». Elles servent de reperes
// pour comparer la ligne avant et apres.
const NoteKind = "note"

// NoteMaxLength borne une note, en caracteres.
const NoteMaxLength = 500

// AddNote inscrit une note au journal.
func AddNote(ctx context.Context, db *sql.DB, at time.Time, text string) error {
	return SaveEvents(ctx, db, []Event{{Time: at, Kind: NoteKind, Message: text}})
}

// DeleteNote retire une note ; les evenements releves ne se suppriment pas.
func DeleteNote(ctx context.Context, db *sql.DB, id int64) (bool, error) {
	res, err := db.ExecContext(ctx, `DELETE FROM events WHERE id = ? AND kind = ?`, id, NoteKind)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// LastAlertTime renvoie l'heure du dernier evenement d'un niveau au moins
// egal a level, zero s'il n'y en a pas.
func LastAlertTime(ctx context.Context, db *sql.DB, level int) (time.Time, error) {
	var ts sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT max(ts) FROM events WHERE level >= ?`, level).Scan(&ts)
	if err != nil || !ts.Valid {
		return time.Time{}, err
	}
	return time.Unix(ts.Int64, 0).UTC(), nil
}
