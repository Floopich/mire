package webview

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/settings"
	"github.com/floopich/mire/internal/store"
)

func TestPenteEtCorrelation(t *testing.T) {
	var xs, ys []float64
	for i := range 20 {
		xs = append(xs, float64(i))
		ys = append(ys, 5-0.05*float64(i)) // −1,5 dB par mois
	}
	if f := slopeFinding("P", "dB", xs, ys); !strings.Contains(f.Text, "En baisse d'environ 1,5") || !f.Alert {
		t.Errorf("baisse : %+v", f)
	}
	flat := make([]float64, 20)
	if f := slopeFinding("P", "dB", xs, flat); !strings.HasPrefix(f.Text, "Stable") {
		t.Errorf("stable : %+v", f)
	}
	if f := slopeFinding("P", "dB", xs[:10], ys[:10]); !strings.Contains(f.Text, "Disponible après") {
		t.Errorf("trop peu de jours : %+v", f)
	}
	// Puissance plus forte par temps froid : −0,1 dB par °C.
	var temps, pw []float64
	for i := range 20 {
		temps = append(temps, float64(i))
		pw = append(pw, 6-0.1*float64(i)+0.05*float64(i%3))
	}
	if f := weatherFinding("P", temps, pw); !strings.Contains(f.Text, "environ 1,0") || !strings.Contains(f.Text, "quand il fait froid") {
		t.Errorf("lien avec la température : %+v", f)
	}
	noise := []float64{1, -1, 1, -1, 1, -1, 1, -1, 1, -1, 1, -1, 1, -1, 1, -1, 1, -1, 1, -1}
	if f := weatherFinding("P", temps, noise); !strings.HasPrefix(f.Text, "Pas de lien net") {
		t.Errorf("sans lien : %+v", f)
	}
}

// baseJours remplit vingt jours : un releve par jour, une puissance qui suit
// une temperature decroissante.
func baseJours(t *testing.T, now time.Time) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	var meteo []store.WeatherDay
	for i := range 20 {
		day := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.Local).AddDate(0, 0, -19+i)
		temp := 15 - float64(i)/2
		raw := docsis.RawSnapshot{DOCSIS: "3.1",
			Downstream: []docsis.RawDownstream{
				{ChannelID: 1, Modulation: "256QAM", Frequency: "442 MHz", PowerLevel: fmt.Sprint(3 - 0.2*temp), MSE: "-40"},
				{ChannelID: 159, Modulation: "OFDM", Frequency: "706 MHz", PowerLevel: "26.6", MSE: "-43"},
			},
			Upstream: []docsis.RawUpstream{{ChannelID: 1, Modulation: "32QAM", Frequency: "37 MHz", PowerLevel: "35"}},
		}
		if _, err := store.SaveSnapshot(ctx, db, raw.Normalize(day)); err != nil {
			t.Fatal(err)
		}
		tm := temp
		meteo = append(meteo, store.WeatherDay{Day: day.Format(time.DateOnly), Mean: &tm})
	}
	if err := store.SaveWeather(ctx, db, meteo); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestPageTendance(t *testing.T) {
	now := time.Now()
	db := baseJours(t, now)
	h, _ := monter(t, db, essai{Collecting: true, Forced: settings.Source{settings.WeatherOn: "true", settings.WeatherPosition: "50.4, 4.8"}})
	cookie := session(t, db)
	rec := get(t, h, cookie, "/tendance?jours=30")
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d", rec.Code)
	}
	body := rec.Body.String()
	for _, voulu := range []string{
		// La moyenne ecarte l'OFDM a 26,6 : la puissance reste sous 3 dBmV.
		"En hausse d&#39;environ 3,0 dB par mois.",
		"Elle suit la température : environ 2,0 dB d&#39;écart pour 10 °C, plus forte quand il fait froid.",
		"Émission</dt><dd class=\"body-l\">Stable",
		`href="/tendance?jours=30" aria-current="true"`,
		"Température extérieure",
	} {
		if !strings.Contains(body, voulu) {
			t.Errorf("absent : %s", voulu)
		}
	}
	if n := strings.Count(body, `class="chart wide"`); n != 4 {
		t.Errorf("%d graphiques, attendu 4", n)
	}
	// Une periode inconnue retombe sur 90 jours ; la vue d'ensemble y mene.
	if !strings.Contains(get(t, h, cookie, "/tendance?jours=7").Body.String(), `href="/tendance?jours=90" aria-current="true"`) {
		t.Error("période par défaut")
	}
	if !strings.Contains(get(t, h, cookie, "/").Body.String(), `href="/tendance"`) {
		t.Error("aucun lien depuis la vue d'ensemble")
	}
}

func TestReperesRonds(t *testing.T) {
	for _, c := range []struct {
		lo, hi       float64
		wantLo, step float64
		want         string
	}{
		{-9.6, 9.6, -10, 5, "−10"},
		{0, 1.4, 0, 0.5, "0,0"},
		{32.1, 41.7, 30, 5, "30"},
		{0.31, 0.47, 0.3, 0.05, "0,30"},
	} {
		lo, hi, step := niceScale(c.lo, c.hi)
		if math.Abs(lo-c.wantLo) > 1e-9 || math.Abs(step-c.step) > 1e-9 || (hi-lo)/step > 4+1e-9 || tickText(lo, step) != c.want {
			t.Errorf("%v..%v : %v..%v pas %v, repère %q", c.lo, c.hi, lo, hi, step, tickText(lo, step))
		}
	}
	for v, want := range map[float64]string{0: "0 %", 0.5: "0,5 %", 1: "1 %", 0.25: "0,25 %"} {
		if got := lossTick(v); got != want {
			t.Errorf("lossTick(%v) = %q", v, got)
		}
	}
}
