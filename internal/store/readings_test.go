package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/floopich/mire/internal/docsis"
)

func baseTest(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatalf("ouverture: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func exemple(at time.Time) docsis.Snapshot {
	raw := docsis.RawSnapshot{
		DOCSIS: "3.1",
		Downstream: []docsis.RawDownstream{
			{ChannelID: 1, Frequency: "650 MHz", Modulation: "256QAM",
				PowerLevel: "1.5", MSE: "-38.9", CorrErrors: 120, NonCorrErrors: 3},
			{ChannelID: 33, Frequency: "800 MHz", Modulation: "OFDM",
				PowerLevel: "2.1", MSE: "", CorrErrors: 7, NonCorrErrors: 2},
		},
		Upstream: []docsis.RawUpstream{
			{ChannelID: 5, Frequency: "42 MHz", Modulation: "64QAM", PowerLevel: "45.5"},
		},
	}
	return raw.Normalize(at)
}

func TestReleveEnregistreEtRelu(t *testing.T) {
	db := baseTest(t)
	ctx := context.Background()
	at := time.Unix(1_700_000_000, 0).UTC()

	if _, err := SaveSnapshot(ctx, db, exemple(at)); err != nil {
		t.Fatalf("enregistrement: %v", err)
	}
	got, err := LatestSnapshot(ctx, db)
	if err != nil {
		t.Fatalf("relecture: %v", err)
	}
	if !got.Timestamp.Equal(at) {
		t.Errorf("horodatage %v, attendu %v", got.Timestamp, at)
	}
	if len(got.Downstream) != 2 || len(got.Upstream) != 1 {
		t.Fatalf("%d descendants et %d montants", len(got.Downstream), len(got.Upstream))
	}
	if got.Downstream[0].SNRDB == nil || *got.Downstream[0].SNRDB != 38.9 {
		t.Errorf("SNR relu: %v", got.Downstream[0].SNRDB)
	}
	// Le canal OFDM n'a pas de MSE : l'absence doit survivre a l'aller-retour
	// en base, sinon il apparaitrait a 0 dB, c'est-a-dire en panne.
	if got.Downstream[1].SNRDB != nil {
		t.Errorf("SNR absent devenu %v", *got.Downstream[1].SNRDB)
	}
}

func TestHistoriqueDUnCanal(t *testing.T) {
	db := baseTest(t)
	ctx := context.Background()
	debut := time.Unix(1_700_000_000, 0).UTC()
	for i := range 5 {
		if _, err := SaveSnapshot(ctx, db, exemple(debut.Add(time.Duration(i)*time.Hour))); err != nil {
			t.Fatalf("releve %d: %v", i, err)
		}
	}
	points, err := DownstreamHistory(ctx, db, 1, debut, debut.Add(10*time.Hour))
	if err != nil {
		t.Fatalf("historique: %v", err)
	}
	if len(points) != 5 {
		t.Fatalf("%d points, attendu 5", len(points))
	}
	for i := 1; i < len(points); i++ {
		if !points[i].Timestamp.After(points[i-1].Timestamp) {
			t.Error("les points ne sont pas ordonnes dans le temps")
		}
	}
	// Une fenetre hors periode ne doit rien rendre.
	vide, err := DownstreamHistory(ctx, db, 1, debut.Add(100*time.Hour), debut.Add(200*time.Hour))
	if err != nil || len(vide) != 0 {
		t.Errorf("fenetre vide: %d points, %v", len(vide), err)
	}
}

func TestBaseVideNEstPasUneErreurDeLecture(t *testing.T) {
	_, err := LatestSnapshot(context.Background(), baseTest(t))
	if err != sql.ErrNoRows {
		t.Errorf("attendu sql.ErrNoRows sur base vide, obtenu %v", err)
	}
}

func TestPurgeParAge(t *testing.T) {
	db := baseTest(t)
	ctx := context.Background()
	maintenant := time.Unix(1_700_000_000, 0).UTC()

	// Un releve d'il y a un an, un d'aujourd'hui.
	if _, err := SaveSnapshot(ctx, db, exemple(maintenant.AddDate(-1, 0, 0))); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveSnapshot(ctx, db, exemple(maintenant)); err != nil {
		t.Fatal(err)
	}

	res, err := Purge(ctx, db, Retention{Days: 30}, maintenant)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if res.ByAge != 1 {
		t.Errorf("%d releves purges, attendu 1", res.ByAge)
	}

	var canaux int
	if err := db.QueryRow("SELECT count(*) FROM ds_channels").Scan(&canaux); err != nil {
		t.Fatal(err)
	}
	// Deux canaux descendants pour le seul releve restant : la cascade doit
	// avoir emporte ceux du releve purge.
	if canaux != 2 {
		t.Errorf("%d canaux restants, attendu 2", canaux)
	}
}

func TestRetentionIllimiteeNePurgeRien(t *testing.T) {
	db := baseTest(t)
	ctx := context.Background()
	vieux := time.Unix(1_000_000_000, 0).UTC()
	if _, err := SaveSnapshot(ctx, db, exemple(vieux)); err != nil {
		t.Fatal(err)
	}
	res, err := Purge(ctx, db, Retention{Days: 0, MaxBytes: 0}, time.Now())
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if res.ByAge != 0 || res.BySize != 0 {
		t.Errorf("une retention illimitee a purge %d + %d releves", res.ByAge, res.BySize)
	}
}

func TestPurgeParTailleRetireLesPlusAnciens(t *testing.T) {
	db := baseTest(t)
	ctx := context.Background()
	debut := time.Unix(1_700_000_000, 0).UTC()

	// Assez de releves pour que les donnees pesent plus que le schema : sur
	// une base minuscule, aucune purge ne peut repasser sous la limite.
	const total = 1500
	for i := range total {
		if _, err := SaveSnapshot(ctx, db, gros(debut.Add(time.Duration(i)*time.Minute))); err != nil {
			t.Fatal(err)
		}
	}
	taille, err := DatabaseSize(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Purge(ctx, db, Retention{MaxBytes: taille / 2}, debut); err != nil {
		t.Fatalf("purge: %v", err)
	}

	var restants int
	var plusAncien int64
	if err := db.QueryRow("SELECT count(*), coalesce(min(ts), 0) FROM readings").Scan(&restants, &plusAncien); err != nil {
		t.Fatal(err)
	}
	if restants == total {
		t.Fatal("la purge par taille n'a rien retire")
	}
	// Les pages liberees restent dans le fichier : si elles etaient comptees,
	// la purge croirait n'avoir rien gagne et viderait tout l'historique.
	if restants < total/3 {
		t.Errorf("%d releves restants sur %d : la purge a retire bien plus que necessaire", restants, total)
	}
	if plusAncien == debut.Unix() {
		t.Error("le releve le plus ancien aurait du partir en premier")
	}
	apres, err := DatabaseSize(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if apres > taille/2 {
		t.Errorf("taille utile %d apres purge, limite %d", apres, taille/2)
	}
}

// gros est un releve de taille realiste : 32 canaux descendants, 4 montants.
func gros(at time.Time) docsis.Snapshot {
	raw := docsis.RawSnapshot{DOCSIS: "3.1"}
	for id := 1; id <= 32; id++ {
		raw.Downstream = append(raw.Downstream, docsis.RawDownstream{
			ChannelID: docsis.FlexInt(id), Frequency: "650 MHz", Modulation: "256QAM",
			PowerLevel: "1.5", MSE: "-38.9", CorrErrors: 120, NonCorrErrors: 3,
		})
	}
	for id := 1; id <= 4; id++ {
		raw.Upstream = append(raw.Upstream, docsis.RawUpstream{
			ChannelID: docsis.FlexInt(id), Frequency: "42 MHz", Modulation: "64QAM", PowerLevel: "45.5",
		})
	}
	return raw.Normalize(at)
}

func TestProjectionDeTaille(t *testing.T) {
	// Dix minutes, dix ans, 48 canaux : l'ordre de grandeur doit rester
	// exploitable pour avertir l'utilisateur a la saisie.
	got := ProjectedSize(600, 3650, 48)
	if got < 1<<30 || got > 4<<30 {
		t.Errorf("projection %d octets, attendu entre 1 et 4 Go", got)
	}
	if ProjectedSize(0, 3650, 48) != 0 {
		t.Error("un intervalle nul doit donner une projection nulle")
	}
}

func TestHistoriqueEtDernierEtatDUnCanalMontant(t *testing.T) {
	db := baseTest(t)
	ctx := context.Background()
	debut := time.Unix(1_700_000_000, 0).UTC()
	for i := range 3 {
		if _, err := SaveSnapshot(ctx, db, exemple(debut.Add(time.Duration(i)*time.Minute))); err != nil {
			t.Fatal(err)
		}
	}
	points, err := UpstreamHistory(ctx, db, 5, debut, debut.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 3 || points[0].Power == nil || *points[0].Power != 45.5 {
		t.Fatalf("historique montant inattendu: %+v", points)
	}
	s, err := UpstreamChannel(ctx, db, 5)
	if err != nil {
		t.Fatal(err)
	}
	if s.Modulation != "64QAM" || !s.LastSeen.Equal(debut.Add(2*time.Minute)) {
		t.Errorf("dernier etat montant inattendu: %+v", s)
	}
	d, err := DownstreamChannel(ctx, db, 33)
	if err != nil {
		t.Fatal(err)
	}
	if d.Modulation != "OFDM" || d.SNR != nil {
		t.Errorf("dernier etat descendant inattendu: %+v", d)
	}
	last, err := LastReadingTime(ctx, db)
	if err != nil || !last.Equal(debut.Add(2*time.Minute)) {
		t.Errorf("dernier releve %v (%v)", last, err)
	}
}

func TestDernierReleveSurBaseVide(t *testing.T) {
	last, err := LastReadingTime(context.Background(), baseTest(t))
	if err != nil || !last.IsZero() {
		t.Errorf("base vide : %v (%v), attendu zero", last, err)
	}
}
