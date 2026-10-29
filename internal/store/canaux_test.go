package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/floopich/mire/internal/docsis"
)

// Les emplacements de recepteur enregistres comme des canaux avant la
// version 7 sont retires ; les vrais canaux restent.
func TestLesFauxCanauxSontRetires(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	snap := docsis.Snapshot{Timestamp: time.Now().UTC(),
		Downstream: []docsis.Downstream{{ChannelID: 1}, {ChannelID: 0}, {ChannelID: 159}},
		Upstream:   []docsis.Upstream{{ChannelID: 5}, {ChannelID: 0}, {ChannelID: 1293955120}},
	}
	if _, err := SaveSnapshot(ctx, db, snap); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 6"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	for table, voulu := range map[string]string{"ds_channels": "1,159", "us_channels": "5"} {
		var got string
		if err := db.QueryRow("SELECT group_concat(channel_id) FROM (SELECT channel_id FROM " + table + " ORDER BY channel_id)").Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != voulu {
			t.Errorf("%s : %s, attendu %s", table, got, voulu)
		}
	}
}
