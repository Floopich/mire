package webview

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/store"
)

// semaine fabrique des tranches de cinq minutes sur plusieurs jours, avec la
// latence et les pertes que donne chaque heure locale.
func semaine(t *testing.T, jours int, latence func(h int) float64, pertes func(h int) int) ([]store.ProbeSample, *time.Location) {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Brussels")
	if err != nil {
		t.Fatal(err)
	}
	debut := time.Date(2026, 10, 1, 0, 0, 0, 0, loc)
	var out []store.ProbeSample
	for at := debut; at.Before(debut.AddDate(0, 0, jours)); at = at.Add(5 * time.Minute) {
		h := at.Hour()
		v := latence(h)
		out = append(out, store.ProbeSample{Start: at.UTC(), Rounds: 10, Failed: pertes(h), RTTAvg: &v})
	}
	return out, loc
}

func TestUnSegmentSatureLeSoirEstSignale(t *testing.T) {
	samples, loc := semaine(t, 10, func(h int) float64 {
		if h >= 20 && h <= 22 {
			return 38
		}
		return 14
	}, func(int) int { return 0 })
	p := hourlyProfile(samples, loc)
	verdict, alert := profileVerdict(p)
	if !alert || !strings.Contains(verdict, "entre 20 h et 23 h") || !strings.Contains(verdict, "38") {
		t.Errorf("verdict %q (alerte %v)", verdict, alert)
	}
}

func TestUneLigneStableLeDit(t *testing.T) {
	samples, loc := semaine(t, 10, func(h int) float64 { return 12 + float64(h%3) }, func(int) int { return 0 })
	verdict, alert := profileVerdict(hourlyProfile(samples, loc))
	if alert || !strings.Contains(verdict, "Latence stable à toute heure") {
		t.Errorf("verdict %q (alerte %v)", verdict, alert)
	}
}

func TestDesPertesALaMemeHeureSontSignalees(t *testing.T) {
	samples, loc := semaine(t, 10, func(int) float64 { return 12 }, func(h int) int {
		if h == 21 {
			return 1 // une tentative sur dix
		}
		return 0
	})
	verdict, alert := profileVerdict(hourlyProfile(samples, loc))
	if !alert || !strings.Contains(verdict, "vers 21 h") {
		t.Errorf("verdict %q (alerte %v)", verdict, alert)
	}
}

func TestDeuxJoursNeFontPasUnProfil(t *testing.T) {
	samples, loc := semaine(t, 2, func(int) float64 { return 12 }, func(int) int { return 0 })
	p := hourlyProfile(samples, loc)
	if verdict, _ := profileVerdict(p); verdict != "" {
		t.Errorf("verdict sur deux jours : %q", verdict)
	}
	if p[12].Days != 2 || p[12].Latency != nil {
		t.Errorf("heure de midi : %+v", p[12])
	}
}

func TestLeProfilSuitLHeureLocale(t *testing.T) {
	// 19 h UTC en octobre (heure d'ete) est 21 h a Bruxelles.
	samples, loc := semaine(t, 5, func(h int) float64 {
		if h == 21 {
			return 50
		}
		return 10
	}, func(int) int { return 0 })
	p := hourlyProfile(samples, loc)
	if p[21].Latency == nil || *p[21].Latency != 50 {
		t.Errorf("21 h locale : %+v", p[21])
	}
}

func TestLaPageInternetMontreLeProfil(t *testing.T) {
	db := baseRemplie(t, 1)
	now := time.Now()
	for at := now.AddDate(0, 0, -6).Truncate(5 * time.Minute); at.Before(now); at = at.Add(5 * time.Minute) {
		v := 15.0
		if h := at.Local().Hour(); h >= 19 && h <= 22 {
			v = 45
		}
		if err := store.SaveProbeSample(t.Context(), db, store.ProbeSample{Start: at, Rounds: 10, RTTAvg: &v, RTTMin: &v, RTTMax: &v}); err != nil {
			t.Fatal(err)
		}
	}
	h, _ := monter(t, db, essai{})
	body := get(t, h, session(t, db), "/internet").Body.String()
	for _, attendu := range []string{"Heure par heure", "Latence plus haute aux heures chargées", `class="bar-heure chargee"`} {
		if !strings.Contains(body, attendu) {
			t.Errorf("page Internet sans %q", attendu)
		}
	}

	vide := baseRemplie(t, 1)
	h, _ = monter(t, vide, essai{})
	if body := get(t, h, session(t, vide), "/internet").Body.String(); !strings.Contains(body, "Disponible après trois jours") {
		t.Error("le profil incomplet n'est pas explique")
	}
}

func TestEchelleRonde(t *testing.T) {
	for in, want := range map[float64]float64{31: 40, 1.02: 2, 0.4: 0.4, 1: 1, 4500: 5000, 9.9: 10, 0.013: 0.02} {
		if got := niceCeil(in); math.Abs(got-want) > 1e-9 {
			t.Errorf("niceCeil(%v) = %v, attendu %v", in, got, want)
		}
	}
}
