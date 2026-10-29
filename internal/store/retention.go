package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Retention decrit la politique de conservation : duree et taille, la
// premiere limite atteinte l'emporte ; zero desactive une limite.
type Retention struct {
	Days     int   // 0 = illimite
	MaxBytes int64 // 0 = illimite
}

// DefaultRetention : dix ans, plafonnes a 5 Go.
var DefaultRetention = Retention{Days: 3650, MaxBytes: 5 << 30}

// PurgeResult dit ce qui a ete retire et pourquoi.
type PurgeResult struct {
	ByAge  int64
	BySize int64
}

// Purge applique la politique de conservation : par age, puis par taille.
func Purge(ctx context.Context, db *sql.DB, r Retention, now time.Time) (PurgeResult, error) {
	var result PurgeResult

	if r.Days > 0 {
		cutoff := now.UTC().AddDate(0, 0, -r.Days).Unix()
		res, err := db.ExecContext(ctx, `DELETE FROM readings WHERE ts < ?`, cutoff)
		if err != nil {
			return result, fmt.Errorf("purge par âge : %w", err)
		}
		result.ByAge, _ = res.RowsAffected()
	}

	if r.MaxBytes > 0 {
		removed, err := purgeToSize(ctx, db, r.MaxBytes)
		if err != nil {
			return result, err
		}
		result.BySize = removed
	}

	// Le journal suit les releves : meme duree de conservation, et rien de
	// plus ancien que le plus ancien releve conserve, qu'on ne pourrait plus
	// recouper. Sans aucun releve, la comparaison est nulle et le journal
	// reste intact : il peut ne contenir que des pannes de collecte.
	if r.Days > 0 {
		cutoff := now.UTC().AddDate(0, 0, -r.Days).Unix()
		for _, q := range []string{
			`DELETE FROM events WHERE ts < ?`,
			// La sonde tourne aussi sans modem configure : ses mesures ne
			// suivent que la duree de conservation.
			`DELETE FROM probe_samples WHERE ts < ?`,
			`DELETE FROM speed_tests WHERE ts < ?`,
			`DELETE FROM link_events WHERE ended_at IS NOT NULL AND ended_at < ?`,
		} {
			if _, err := db.ExecContext(ctx, q, cutoff); err != nil {
				return result, fmt.Errorf("purge du journal : %w", err)
			}
		}
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM events WHERE ts < (SELECT min(ts) FROM readings)`); err != nil {
		return result, fmt.Errorf("purge du journal : %w", err)
	}
	return result, nil
}

// purgeToSize retire les releves les plus anciens par tranches, jusqu'a
// repasser sous la limite.
func purgeToSize(ctx context.Context, db *sql.DB, maxBytes int64) (int64, error) {
	const batch = 500
	var total int64
	for {
		size, err := DatabaseSize(ctx, db)
		if err != nil {
			return total, err
		}
		if size <= maxBytes {
			return total, nil
		}
		res, err := db.ExecContext(ctx,
			`DELETE FROM readings WHERE id IN (
				SELECT id FROM readings ORDER BY ts ASC LIMIT ?)`, batch)
		if err != nil {
			return total, fmt.Errorf("purge par taille: %w", err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			// Plus rien a supprimer : la base depasse la limite par son seul
			// schema. Mieux vaut s'arreter que boucler indefiniment.
			return total, nil
		}
		total += n
	}
}

// DatabaseSize renvoie la place occupee par les donnees, en octets, sans les
// pages libres.
func DatabaseSize(ctx context.Context, db *sql.DB) (int64, error) {
	var pageCount, freePages, pageSize int64
	if err := db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil {
		return 0, err
	}
	if err := db.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&freePages); err != nil {
		return 0, err
	}
	if err := db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return 0, err
	}
	return (pageCount - freePages) * pageSize, nil
}

// ProjectedSize estime la taille atteinte par une politique donnee.
func ProjectedSize(intervalSeconds, days, channels int) int64 {
	if intervalSeconds <= 0 || days <= 0 || channels <= 0 {
		return 0
	}
	const bytesPerRow = 84 // ligne plus surcout d'index, mesure sur un echantillon
	perDay := int64(86400/intervalSeconds) * int64(channels)
	return perDay * int64(days) * bytesPerRow
}
