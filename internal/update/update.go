// Package update verifie, une fois par jour et seulement si l'utilisateur
// l'a demande, si une version plus recente de Mire est publiee.
package update

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/store"
)

// LatestURL est la derniere version publiee, hors pre-versions.
const LatestURL = "https://api.github.com/repos/floopich/mire/releases/latest"

// ReleasesURL est la page des versions.
const ReleasesURL = "https://github.com/floopich/mire/releases"

// KindNewVersion est l'evenement du journal qui annonce une version.
const KindNewVersion = "nouvelle_version"

const (
	every     = 24 * time.Hour
	retry     = time.Hour
	firstWait = time.Minute
	// maxBody borne la reponse lue : quelques kilo-octets suffisent.
	maxBody = 1 << 20
)

var semver = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

// Release est la derniere version publiee.
type Release struct {
	Version string // « 1.6.1 »
	URL     string
}

// Checker interroge GitHub et garde le resultat en memoire.
type Checker struct {
	DB *sql.DB
	// Current est la version en cours ; une version de developpement n'est
	// comparee a rien.
	Current string
	URL     string
	HTTP    *http.Client
	Logger  *slog.Logger

	mu      sync.Mutex
	latest  Release
	checked time.Time
	err     error
}

// New prepare une verification vers GitHub.
func New(db *sql.DB, current string, logger *slog.Logger) *Checker {
	return &Checker{DB: db, Current: current, URL: LatestURL, HTTP: &http.Client{Timeout: 20 * time.Second}, Logger: logger}
}

// Status renvoie la derniere version connue, si elle est plus recente que
// la version en cours, et l'heure et l'erreur de la derniere verification.
func (c *Checker) Status() (r Release, newer bool, checked time.Time, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.latest, c.latest.Version != "" && Newer(c.latest.Version, c.Current), c.checked, c.err
}

// Run verifie peu apres le demarrage, puis chaque jour.
func (c *Checker) Run(ctx context.Context) error {
	wait := firstWait
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		wait = every
		if err := c.Check(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			c.Logger.Warn("vérification des versions impossible", "erreur", err)
			wait = retry
		}
	}
}

// Check interroge GitHub une fois. Une version plus recente, vue pour la
// premiere fois, s'inscrit au journal.
func (c *Checker) Check(ctx context.Context) error {
	r, err := c.fetch(ctx)
	c.mu.Lock()
	c.checked, c.err = time.Now(), err
	if err == nil {
		c.latest = r
	}
	c.mu.Unlock()
	if err != nil || !Newer(r.Version, c.Current) {
		return err
	}
	msg := "Nouvelle version de Mire : " + r.Version + "."
	last, seen, err := store.LastEvent(ctx, c.DB, KindNewVersion)
	if err != nil || (seen && last.Message == msg) {
		return err
	}
	c.Logger.Info("nouvelle version disponible", "version", r.Version)
	return store.SaveEvents(ctx, c.DB, []store.Event{{Time: time.Now(), Kind: KindNewVersion,
		Level: int(analyze.Unknown), Message: msg}})
}

func (c *Checker) fetch(ctx context.Context) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Mire")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Release{}, errors.New("GitHub injoignable")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("GitHub répond HTTP %d", resp.StatusCode)
	}
	var body struct {
		Tag string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&body); err != nil {
		return Release{}, errors.New("réponse de GitHub illisible")
	}
	m := semver.FindStringSubmatch(body.Tag)
	if m == nil {
		return Release{}, errors.New("numéro de version illisible")
	}
	v := m[1] + "." + m[2] + "." + m[3]
	// L'adresse se construit ici : rien de la reponse ne devient un lien.
	return Release{Version: v, URL: ReleasesURL + "/tag/v" + v}, nil
}

// Newer dit si la version a est plus recente que b. Une version illisible,
// comme une version de developpement, n'est jamais comparee.
func Newer(a, b string) bool {
	pa, pb := parse(a), parse(b)
	if pa == nil || pb == nil {
		return false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

// Comparable dit si une version se compare aux versions publiees : une
// version de developpement ne se compare a rien.
func Comparable(v string) bool { return parse(v) != nil }

func parse(v string) []int {
	m := semver.FindStringSubmatch(v)
	if m == nil {
		return nil
	}
	out := make([]int, 3)
	for i := range out {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out
}
