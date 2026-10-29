// Package speedtest lit des mesures de debit faites par Speedtest Tracker
// (API v1, jeton en lecture seule) ou par une passerelle UniFi, et les garde
// en base.
package speedtest

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

const (
	// refresh est le rythme de lecture : Speedtest Tracker mesure au mieux
	// toutes les quelques minutes, le plus souvent toutes les heures.
	refresh = 15 * time.Minute
	// pageSize est le nombre de mesures par page de l'API (500 au plus).
	pageSize = 100
	// firstPages borne la premiere lecture : 2 000 mesures, plus de deux
	// mois de mesures horaires.
	firstPages = 20
)

// Source rend les mesures posterieures a since, la plus recente d'abord ;
// maxPages borne une premiere lecture d'historique.
type Source interface {
	Since(ctx context.Context, since time.Time, maxPages int) ([]store.SpeedTest, error)
}

// Client interroge un Speedtest Tracker.
type Client struct {
	// URL est l'adresse de l'instance, sans /api.
	URL   string
	Token string
	HTTP  *http.Client
}

// NewClient prepare un client.
func NewClient(address, token string) Client {
	return Client{URL: strings.TrimRight(strings.TrimSpace(address), "/"), Token: strings.TrimSpace(token),
		HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// result est une mesure telle que l'API la rend.
type result struct {
	Download  *float64 `json:"download"` // octets par seconde
	Upload    *float64 `json:"upload"`
	Ping      *float64 `json:"ping"`
	Status    string   `json:"status"`
	CreatedAt string   `json:"created_at"`
	Data      struct {
		Timestamp  string   `json:"timestamp"`
		PacketLoss *float64 `json:"packetLoss"`
		Ping       struct {
			Jitter *float64 `json:"jitter"`
		} `json:"ping"`
		Server struct {
			Name     string `json:"name"`
			Location string `json:"location"`
		} `json:"server"`
	} `json:"data"`
}

// test convertit une mesure reussie ; ok est faux pour une mesure echouee
// ou incomplete.
func (r result) test() (store.SpeedTest, bool) {
	if r.Status != "completed" || r.Download == nil || r.Upload == nil || *r.Download <= 0 {
		return store.SpeedTest{}, false
	}
	// L'horodatage de la mesure elle-meme, en UTC ; a defaut, la date
	// d'enregistrement, que Speedtest Tracker garde en UTC par defaut.
	at, err := time.Parse(time.RFC3339, r.Data.Timestamp)
	if err != nil {
		if at, err = time.Parse(time.DateTime, r.CreatedAt); err != nil {
			return store.SpeedTest{}, false
		}
	}
	server := r.Data.Server.Name
	if l := r.Data.Server.Location; l != "" && server != "" {
		server += " (" + l + ")"
	}
	return store.SpeedTest{
		Time:     at.UTC(),
		Download: mbps(*r.Download), Upload: mbps(*r.Upload),
		Ping: r.Ping, Jitter: r.Data.Ping.Jitter, Loss: r.Data.PacketLoss, Server: server,
	}, true
}

// mbps convertit des octets par seconde en megabits par seconde, au dixieme.
func mbps(bytesPerSecond float64) float64 {
	v := bytesPerSecond * 8 / 1e6
	return float64(int64(v*10+0.5)) / 10
}

// page lit une page de mesures, les plus recentes d'abord.
func (c Client) page(ctx context.Context, n int) (results []result, last bool, err error) {
	if c.URL == "" {
		return nil, false, errors.New("adresse de Speedtest Tracker non renseignée")
	}
	if c.Token == "" {
		return nil, false, errors.New("jeton d'API non renseigné")
	}
	q := url.Values{"sort": {"-created_at"}, "page[size]": {strconv.Itoa(pageSize)}, "page[number]": {strconv.Itoa(n)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL+"/api/v1/results?"+q.Encode(), nil)
	if err != nil {
		return nil, false, fmt.Errorf("adresse de Speedtest Tracker invalide : %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("Speedtest Tracker ne répond pas à %s", c.URL)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, false, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return nil, false, errors.New("Speedtest Tracker refuse le jeton d'API")
	case http.StatusForbidden:
		return nil, false, errors.New("le jeton d'API n'a pas le droit de lire les résultats (« Read results »)")
	case http.StatusNotFound:
		return nil, false, errors.New("aucune API de Speedtest Tracker à cette adresse : vérifiez-la, et que la version installée est récente")
	default:
		return nil, false, fmt.Errorf("Speedtest Tracker a répondu HTTP %d", resp.StatusCode)
	}
	var r struct {
		Data []result `json:"data"`
		Meta struct {
			CurrentPage int `json:"current_page"`
			LastPage    int `json:"last_page"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, false, errors.New("réponse illisible : est-ce bien Speedtest Tracker à cette adresse ?")
	}
	return r.Data, r.Meta.CurrentPage >= r.Meta.LastPage || len(r.Data) == 0, nil
}

// Since renvoie les mesures reussies posterieures a since, au plus maxPages
// pages ; la plus recente d'abord.
func (c Client) Since(ctx context.Context, since time.Time, maxPages int) ([]store.SpeedTest, error) {
	var out []store.SpeedTest
	for n := 1; n <= maxPages; n++ {
		results, last, err := c.page(ctx, n)
		if err != nil {
			return out, err
		}
		for _, r := range results {
			t, ok := r.test()
			if !ok {
				continue
			}
			if !t.Time.After(since) {
				return out, nil
			}
			out = append(out, t)
		}
		if last {
			break
		}
	}
	return out, nil
}

// Fetcher garde la base a jour.
type Fetcher struct {
	DB     *sql.DB
	Source Source
	Logger *slog.Logger

	mu   sync.Mutex
	last time.Time
	err  error
}

// Status renvoie l'heure de la derniere lecture reussie et la derniere
// erreur.
func (f *Fetcher) Status() (time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last, f.err
}

// Run lit les nouvelles mesures au demarrage puis tous les quarts d'heure.
func (f *Fetcher) Run(ctx context.Context) error {
	if f.Logger == nil {
		f.Logger = slog.Default()
	}
	for {
		if n, err := f.Update(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			f.Logger.Warn("débit", "erreur", err)
		} else if n > 0 {
			f.Logger.Info("débit : nouvelles mesures", "mesures", n)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(refresh):
		}
	}
}

// Update enregistre les mesures nouvelles et renvoie leur nombre. C'est
// aussi l'essai des Reglages.
func (f *Fetcher) Update(ctx context.Context) (int, error) {
	since, err := store.LastSpeedTestTime(ctx, f.DB)
	if err == nil {
		var tests []store.SpeedTest
		if tests, err = f.Source.Since(ctx, since, firstPages); err == nil {
			var n int
			if n, err = store.SaveSpeedTests(ctx, f.DB, tests); err == nil {
				f.mu.Lock()
				f.last, f.err = time.Now(), nil
				f.mu.Unlock()
				return n, nil
			}
		}
	}
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
	return 0, err
}
