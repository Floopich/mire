package webview

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/events"
	"github.com/floopich/mire/internal/store"
)

func TestPeriode(t *testing.T) {
	now := time.Date(2026, 11, 3, 20, 0, 0, 0, time.Local)
	req := func(q string) *http.Request { return httptest.NewRequest(http.MethodGet, "/internet?"+q, nil) }

	p := periodFor(req("heures=24"), "/internet", 24, now)
	if !p.Live || !p.To.Equal(now) || !p.From.Equal(now.Add(-24*time.Hour)) || p.Next != "" {
		t.Errorf("maintenant : %+v", p)
	}
	if p.Prev != "/internet?fin=2026-11-02T20%3A00&heures=24" {
		t.Errorf("précédente : %s", p.Prev)
	}
	p = periodFor(req("heures=24&fin=2026-11-01T08:30"), "/internet", 24, now)
	if p.Live || p.To.Format(finLayout) != "2026-11-01T08:30" || p.Next != "/internet?fin=2026-11-02T08%3A30&heures=24" ||
		p.Label != "du 31/10/2026 à 08:30 au 01/11/2026 à 08:30" {
		t.Errorf("passée : %+v", p)
	}
	// La suivante d'une periode qui touche maintenant revient a maintenant.
	p = periodFor(req("heures=24&fin=2026-11-03T10:00"), "/internet", 24, now)
	if p.Next != "/internet?heures=24" {
		t.Errorf("suivante : %s", p.Next)
	}
	for _, q := range []string{"fin=2026-12-01T00:00", "fin=n%27importe", "fin="} {
		if p := periodFor(req("heures=6&"+q), "/internet", 6, now); !p.Live {
			t.Errorf("%s : %+v", q, p)
		}
	}
}

// baseChrono : six releves a dix minutes, le canal 2 absent du troisieme ;
// une coupure et un passage en avertissement.
func baseChrono(t *testing.T, now time.Time) *storeDB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	for i := range 6 {
		raw := docsis.RawSnapshot{DOCSIS: "3.1",
			Downstream: []docsis.RawDownstream{
				{ChannelID: 1, Modulation: "256QAM", Frequency: "650 MHz", PowerLevel: "1.5", MSE: "-38.9",
					NonCorrErrors: docsis.FlexInt(100 * i)},
				{ChannelID: 2, Modulation: "256QAM", Frequency: "658 MHz", PowerLevel: "4.5", MSE: "-36.1",
					NonCorrErrors: docsis.FlexInt(10 * i)},
			},
			Upstream: []docsis.RawUpstream{{ChannelID: 5, PowerLevel: "45.5"}}}
		if i == 2 {
			raw.Downstream = raw.Downstream[:1]
		}
		if _, err := store.SaveSnapshot(ctx, db, raw.Normalize(now.Add(-time.Duration(5-i)*10*time.Minute))); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveEvents(ctx, db, []store.Event{{Time: now.Add(-25 * time.Minute), Kind: events.KindState, Level: 3,
		Message: "Ligne à surveiller : essai."}}); err != nil {
		t.Fatal(err)
	}
	id, err := store.OpenOutage(ctx, db, "ligne", now.Add(-40*time.Minute), "Coupure de la ligne : essai.")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CloseOutage(ctx, db, id, now.Add(-38*time.Minute)); err != nil {
		t.Fatal(err)
	}
	return &storeDB{db}
}

func TestPointsDeLaLigne(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	b := baseChrono(t, now)
	pts, err := store.LinePoints(context.Background(), b.DB, now.Add(-time.Hour), now)
	if err != nil || len(pts) != 6 {
		t.Fatalf("%d points, %v", len(pts), err)
	}
	if *pts[0].DSPowerMin != 1.5 || *pts[0].DSPowerMax != 4.5 || *pts[0].SNRMin != 36.1 || *pts[0].USPowerMax != 45.5 {
		t.Errorf("extrêmes : %+v", pts[0])
	}
	// Le premier releve n'a pas de point de comparaison ; ensuite, les deux
	// canaux comptent, sauf le canal 2 juste apres son absence.
	want := []int64{0, 110, 100, 100, 110, 110}
	for i, p := range pts {
		if p.Uncorrected != want[i] || p.Known != (i > 0) {
			t.Errorf("relevé %d : %d erreurs (connu %v), attendu %d", i, p.Uncorrected, p.Known, want[i])
		}
	}
}

func TestChronologie(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	b := baseChrono(t, now)
	h, _ := monter(t, b.DB, essai{Collecting: true, Interval: 10 * time.Minute, Now: func() time.Time { return now }})
	tok := session(t, b.DB)

	body := html.UnescapeString(get(t, h, tok, "/chronologie?heures=6").Body.String())
	for _, voulu := range []string{
		"État de la ligne", "Coupures et événements", "Puissance descendante", "SNR le plus bas",
		"Émission la plus forte", "Erreurs non corrigées",
		`class="avertissement"><title>À surveiller depuis`,
		"Coupure de la ligne : essai (2\u00a0min)",
		`href="/releve?t=`,
		"Période précédente",
	} {
		if !strings.Contains(body, voulu) {
			t.Errorf("chronologie, absent : %s", voulu)
		}
	}
	if get(t, h, "", "/chronologie").Code != http.StatusSeeOther {
		t.Error("chronologie sans session")
	}

	// Le releve d'un instant : celui de la minute, ou le dernier avant.
	at := now.Add(-20 * time.Minute)
	body = html.UnescapeString(get(t, h, tok, releveURL(at)).Body.String())
	for _, voulu := range []string{"Relevé du " + FmtTime(at), "Relevé précédent", "Relevé suivant", "Jugé avec les seuils actuels.",
		"/descendant/1?fin="} {
		if !strings.Contains(body, voulu) {
			t.Errorf("relevé, absent : %s", voulu)
		}
	}
	if rec := get(t, h, tok, releveURL(now.Add(-24*time.Hour))); rec.Code != http.StatusNotFound {
		t.Errorf("avant le premier relevé : HTTP %d", rec.Code)
	}
	if rec := get(t, h, tok, "/releve?t=hier"); rec.Code != http.StatusBadRequest {
		t.Errorf("date illisible : HTTP %d", rec.Code)
	}
}
