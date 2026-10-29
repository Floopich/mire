// Package weather releve la temperature journaliere d'un lieu sur
// Open-Meteo, sans compte ; seule la position est envoyee.
package weather

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/floopich/mire/internal/store"
)

// Adresses des services d'Open-Meteo, remplacables dans les tests.
const (
	DefaultGeocoding = "https://geocoding-api.open-meteo.com/v1/search"
	DefaultForecast  = "https://api.open-meteo.com/v1/forecast"
)

const (
	// pastDays est le plus long historique que le service de prevision
	// accepte ; les journees deja enregistrees restent en base au-dela.
	pastDays = 92
	refresh  = 6 * time.Hour
	retry    = 30 * time.Minute
)

// Place est un lieu resolu.
type Place struct {
	Name     string
	Lat, Lon float64
}

// Client interroge Open-Meteo.
type Client struct {
	HTTP      *http.Client
	Geocoding string
	Forecast  string
}

// NewClient renvoie un client vers les services publics.
func NewClient() Client {
	return Client{HTTP: &http.Client{Timeout: 20 * time.Second}, Geocoding: DefaultGeocoding, Forecast: DefaultForecast}
}

// Resolve transforme la position saisie en lieu : des coordonnees
// « 50.47, 4.87 », ou un nom de commune. Entre homonymes, le pays du profil
// d'operateur l'emporte.
func (c Client) Resolve(ctx context.Context, position, country string) (Place, error) {
	position = strings.TrimSpace(position)
	if position == "" {
		return Place{}, errors.New("position non renseignée")
	}
	if p, ok := ParseCoordinates(position); ok {
		return p, nil
	}
	// Le service cherche un nom de lieu seul : « Namur, Belgique » devient
	// « Namur », le pays du profil departageant les homonymes.
	name, _, _ := strings.Cut(position, ",")
	q := url.Values{"name": {strings.TrimSpace(name)}, "count": {"10"}, "language": {"fr"}, "format": {"json"}}
	var r struct {
		Results []struct {
			Name        string  `json:"name"`
			Latitude    float64 `json:"latitude"`
			Longitude   float64 `json:"longitude"`
			CountryCode string  `json:"country_code"`
			Country     string  `json:"country"`
			Admin1      string  `json:"admin1"`
			Admin2      string  `json:"admin2"`
		} `json:"results"`
	}
	if err := c.get(ctx, c.Geocoding+"?"+q.Encode(), &r); err != nil {
		return Place{}, err
	}
	if len(r.Results) == 0 {
		return Place{}, fmt.Errorf("commune « %s » introuvable : essayez des coordonnées, par exemple 50.47, 4.87", position)
	}
	best := r.Results[0]
	for _, x := range r.Results {
		if country != "" && strings.EqualFold(x.CountryCode, country) {
			best = x
			break
		}
	}
	label := best.Name
	for _, part := range []string{best.Admin2, best.Country} {
		if part != "" && part != best.Name {
			label += ", " + part
		}
	}
	return Place{Name: label, Lat: best.Latitude, Lon: best.Longitude}, nil
}

// ParseCoordinates lit « 50.47, 4.87 » ou « 50,47 ; 4,87 ».
func ParseCoordinates(s string) (Place, bool) {
	sep := ","
	if strings.Contains(s, ";") {
		sep = ";"
	} else if strings.Count(s, ",") != 1 {
		f := strings.Fields(s)
		if len(f) != 2 {
			return Place{}, false
		}
		s, sep = f[0]+";"+f[1], ";"
	}
	a, b, ok := strings.Cut(s, sep)
	if !ok {
		return Place{}, false
	}
	num := func(v string) (float64, error) {
		return strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(v), ",", "."), 64)
	}
	lat, err1 := num(a)
	lon, err2 := num(b)
	if err1 != nil || err2 != nil || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return Place{}, false
	}
	return Place{Name: strconv.FormatFloat(lat, 'f', 2, 64) + ", " + strconv.FormatFloat(lon, 'f', 2, 64), Lat: lat, Lon: lon}, true
}

// Daily renvoie les temperatures journalieres des derniers jours, a l'heure
// du lieu. La moyenne est celle du minimum et du maximum, l'usage
// climatologique courant.
func (c Client) Daily(ctx context.Context, p Place, days int) ([]store.WeatherDay, error) {
	q := url.Values{
		"latitude":      {strconv.FormatFloat(p.Lat, 'f', 4, 64)},
		"longitude":     {strconv.FormatFloat(p.Lon, 'f', 4, 64)},
		"daily":         {"temperature_2m_max,temperature_2m_min"},
		"past_days":     {strconv.Itoa(days)},
		"forecast_days": {"1"},
		"timezone":      {"auto"},
	}
	var r struct {
		Daily struct {
			Time []string   `json:"time"`
			Max  []*float64 `json:"temperature_2m_max"`
			Min  []*float64 `json:"temperature_2m_min"`
		} `json:"daily"`
	}
	if err := c.get(ctx, c.Forecast+"?"+q.Encode(), &r); err != nil {
		return nil, err
	}
	d := r.Daily
	if len(d.Max) != len(d.Time) || len(d.Min) != len(d.Time) {
		return nil, errors.New("réponse d'Open-Meteo incomplète")
	}
	out := make([]store.WeatherDay, 0, len(d.Time))
	for i, day := range d.Time {
		w := store.WeatherDay{Day: day, Min: d.Min[i], Max: d.Max[i]}
		if w.Min != nil && w.Max != nil {
			m := (*w.Min + *w.Max) / 2
			w.Mean = &m
		}
		out = append(out, w)
	}
	return out, nil
}

func (c Client) get(ctx context.Context, target string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mire")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return fmt.Errorf("Open-Meteo injoignable : %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Open-Meteo répond HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return errors.New("réponse d'Open-Meteo illisible")
	}
	return nil
}

// Fetcher tient la meteo a jour tant que son contexte vit.
type Fetcher struct {
	DB       *sql.DB
	Client   Client
	Position string
	Country  string
	Logger   *slog.Logger

	mu    sync.Mutex
	place Place
	last  time.Time
	err   error
}

// Status renvoie le lieu resolu, l'heure de la derniere mise a jour et la
// derniere erreur.
func (f *Fetcher) Status() (Place, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.place, f.last, f.err
}

// Run releve la meteo au demarrage puis toutes les six heures ; apres un
// echec, il reessaie une demi-heure plus tard.
func (f *Fetcher) Run(ctx context.Context) error {
	if f.Logger == nil {
		f.Logger = slog.Default()
	}
	for {
		wait := refresh
		if _, _, err := f.Update(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			f.Logger.Warn("météo", "erreur", err)
			wait = retry
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// Update resout le lieu, releve les temperatures et les enregistre ; il
// renvoie le lieu et le nombre de journees. C'est aussi l'essai des Reglages.
func (f *Fetcher) Update(ctx context.Context) (Place, int, error) {
	p, err := f.Client.Resolve(ctx, f.Position, f.Country)
	if err == nil {
		var days []store.WeatherDay
		if days, err = f.Client.Daily(ctx, p, pastDays); err == nil {
			if err = store.SaveWeather(ctx, f.DB, days); err == nil {
				f.mu.Lock()
				f.place, f.last, f.err = p, time.Now(), nil
				f.mu.Unlock()
				return p, len(days), nil
			}
		}
	}
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
	return Place{}, 0, err
}
