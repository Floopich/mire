package store

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

func exporter(t *testing.T, db *sql.DB, name string, from, to time.Time) []string {
	t.Helper()
	e, ok := ExportByName(name)
	if !ok {
		t.Fatalf("tableau %q inconnu", name)
	}
	var out []string
	if err := e.Write(context.Background(), db, from, to, func(r []string) error {
		if len(r) != len(e.Header) {
			t.Errorf("%s : %d cellules pour %d colonnes", name, len(r), len(e.Header))
		}
		out = append(out, strings.Join(r, "|"))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestExportDesCanauxDescendants(t *testing.T) {
	db := baseTest(t)
	ctx := context.Background()
	t0 := time.Unix(1_700_000_000, 0).UTC()
	for i, c := range [][2]int64{{1000, 10}, {1100, 12}, {40, 2}} {
		if _, err := SaveSnapshot(ctx, db, releveErreurs(t0.Add(time.Duration(i)*10*time.Minute), 3, c[0], c[1])); err != nil {
			t.Fatal(err)
		}
	}
	got := exporter(t, db, "descendant", t0, t0.Add(time.Hour))
	if len(got) != 4 || got[0] != "horodatage|canal|frequence_mhz|modulation|puissance_dbmv|snr_db|erreurs_corrigees|erreurs_non_corrigees" {
		t.Fatalf("export : %q", got)
	}
	// Premier releve : pas d'accroissement connu ; puis 100 et 2 ; puis le
	// modem a redemarre, les compteurs repartent de zero.
	want := []string{
		stamp(t0.Unix()) + "|3||256QAM||||",
		stamp(t0.Add(10*time.Minute).Unix()) + "|3||256QAM|||100|2",
		stamp(t0.Add(20*time.Minute).Unix()) + "|3||256QAM|||40|2",
	}
	for i, w := range want {
		if got[i+1] != w {
			t.Errorf("ligne %d : %q, attendu %q", i+1, got[i+1], w)
		}
	}
	// La fin de periode est exclue.
	if n := len(exporter(t, db, "descendant", t0, t0.Add(20*time.Minute))); n != 3 {
		t.Errorf("%d lignes avant la fin de période", n)
	}
}

func TestExportDuDebitEtDesCoupures(t *testing.T) {
	db := baseTest(t)
	ctx := context.Background()
	t0 := time.Unix(1_700_000_000, 0).UTC()
	ping := 12.5
	tests := []SpeedTest{{Time: t0, Download: 950.2, Upload: 48.1, Ping: &ping, Server: "Orange Belgium"},
		{Time: t0.Add(time.Hour), Download: 900, Upload: 50}}
	if n, err := SaveSpeedTests(ctx, db, tests); err != nil || n != 2 {
		t.Fatalf("%d mesures, %v", n, err)
	}
	// Relire les memes mesures n'ajoute rien.
	if n, _ := SaveSpeedTests(ctx, db, tests); n != 0 {
		t.Errorf("%d doublons", n)
	}
	got := exporter(t, db, "debit", t0, t0.Add(2*time.Hour))
	if len(got) != 3 || got[1] != stamp(t0.Unix())+"|950.2|48.1|12.5|||Orange Belgium" || got[2] != stamp(t0.Add(time.Hour).Unix())+"|900|50||||" {
		t.Errorf("débit : %q", got)
	}
	if last, _ := LastSpeedTestTime(ctx, db); !last.Equal(t0.Add(time.Hour)) {
		t.Errorf("dernière mesure %v", last)
	}

	id, err := OpenOutage(ctx, db, "ligne", t0.Add(time.Minute), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := CloseOutage(ctx, db, id, t0.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenOutage(ctx, db, "local", t0.Add(time.Hour), "modem muet"); err != nil {
		t.Fatal(err)
	}
	got = exporter(t, db, "coupures", t0, t0.Add(2*time.Hour))
	want := []string{"debut|fin|duree_s|cause|detail",
		stamp(t0.Add(time.Minute).Unix()) + "|" + stamp(t0.Add(4*time.Minute).Unix()) + "|180|ligne|",
		stamp(t0.Add(time.Hour).Unix()) + "|||local|modem muet"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("coupures : %q", got)
	}
}
