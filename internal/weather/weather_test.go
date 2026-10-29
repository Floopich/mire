package weather

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/floopich/mire/internal/store"
)

func TestCoordonnees(t *testing.T) {
	for in, ok := range map[string]bool{
		"50.47, 4.87": true, "50,47 ; 4,87": true, "50.4 4.87": true, "-33.9;151.2": true,
		"Dinant": false, "Namur, Belgique": false, "95, 5": false, "50.4": false,
	} {
		p, got := ParseCoordinates(in)
		if got != ok {
			t.Errorf("%q : %v, attendu %v", in, got, ok)
		}
		if ok && (p.Lat == 0 || p.Lon == 0) {
			t.Errorf("%q : %+v", in, p)
		}
	}
}

// faux reproduit les deux services d'Open-Meteo.
func faux(t *testing.T) (Client, *[]string) {
	t.Helper()
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.String())
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/geo":
			if r.URL.Query().Get("name") == "Nullepart" {
				_, _ = w.Write([]byte(`{"generationtime_ms":0.5}`))
				return
			}
			_, _ = w.Write([]byte(`{"results":[
				{"name":"Namur","latitude":45.9,"longitude":-74.9,"country_code":"CA","country":"Canada"},
				{"name":"Namur","latitude":50.47,"longitude":4.87,"country_code":"BE","country":"Belgique","admin2":"Province de Namur"}]}`))
		case "/prev":
			_, _ = w.Write([]byte(`{"daily":{"time":["2026-10-25","2026-10-26","2026-10-27"],
				"temperature_2m_max":[14.0,12.5,null],"temperature_2m_min":[6.0,4.5,null]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return Client{HTTP: srv.Client(), Geocoding: srv.URL + "/geo", Forecast: srv.URL + "/prev"}, &asked
}

func TestCommuneResolueDansLePaysDuProfil(t *testing.T) {
	c, asked := faux(t)
	p, err := c.Resolve(context.Background(), "Namur, Belgique", "BE")
	if err != nil {
		t.Fatal(err)
	}
	if p.Lat != 50.47 || p.Name != "Namur, Province de Namur, Belgique" {
		t.Errorf("lieu : %+v", p)
	}
	if q := (*asked)[0]; !strings.Contains(q, "name=Namur") || strings.Contains(q, "Belgique") {
		t.Errorf("le nom cherché garde la précision du pays : %s", (*asked)[0])
	}
	if _, err := c.Resolve(context.Background(), "Nullepart", "BE"); err == nil || !strings.Contains(err.Error(), "introuvable") {
		t.Errorf("commune inconnue : %v", err)
	}
	// Des coordonnees ne sont pas envoyees au geocodage.
	n := len(*asked)
	if _, err := c.Resolve(context.Background(), "50.4, 4.8", "BE"); err != nil || len(*asked) != n {
		t.Errorf("coordonnées : %v, %d requêtes", err, len(*asked)-n)
	}
}

func TestReleveEnregistre(t *testing.T) {
	c, asked := faux(t)
	db, err := store.Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	f := &Fetcher{DB: db, Client: c, Position: "50.47, 4.87", Country: "BE"}
	p, n, err := f.Update(context.Background())
	if err != nil || n != 3 || p.Lat != 50.47 {
		t.Fatalf("relevé : %+v, %d, %v", p, n, err)
	}
	if q := (*asked)[0]; !strings.Contains(q, "past_days=92") || !strings.Contains(q, "timezone=auto") {
		t.Errorf("requête : %s", q)
	}
	days, err := store.Weather(context.Background(), db, "2026-10-25", "2026-10-27")
	if err != nil || len(days) != 3 {
		t.Fatalf("en base : %+v, %v", days, err)
	}
	if *days[0].Mean != 10 || *days[1].Mean != 8.5 || days[2].Mean != nil {
		t.Errorf("moyennes : %v %v %v", *days[0].Mean, *days[1].Mean, days[2].Mean)
	}
	if _, last, err := f.Status(); last.IsZero() || err != nil {
		t.Errorf("état : %v, %v", last, err)
	}
	// Un second releve remplace les journees, sans doublon.
	if _, _, err := f.Update(context.Background()); err != nil {
		t.Fatal(err)
	}
	if days, _ := store.Weather(context.Background(), db, "2026-01-01", "2026-12-31"); len(days) != 3 {
		t.Errorf("%d journées après un second relevé", len(days))
	}
}
