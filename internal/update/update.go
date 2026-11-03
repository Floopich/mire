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

// ListURL liste les versions, versions de test comprises : une version de
// test se compare a toutes.
const ListURL = "https://api.github.com/repos/floopich/mire/releases?per_page=20"

// KindNewVersion est l'evenement du journal qui annonce une version.
const KindNewVersion = "nouvelle_version"

const (
	every     = 24 * time.Hour
	retry     = time.Hour
	firstWait = time.Minute
	// maxBody borne la reponse lue : quelques kilo-octets suffisent.
	maxBody = 1 << 20
)

// semver reconnait 1.9.0 et les versions de test 1.9.0-rc1 ; une version
// de developpement (main-98b4393, 1.8.0+dirty) n'y repond pas.
var semver = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z]+(?:\.[0-9A-Za-z]+)*))?$`)

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
	// URL donne la derniere version stable ; ListURL toutes les versions,
	// lues quand la version en cours est une version de test.
	URL, ListURL string
	HTTP         *http.Client
	Logger       *slog.Logger

	mu      sync.Mutex
	latest  Release
	checked time.Time
	err     error
}

// New prepare une verification vers GitHub.
func New(db *sql.DB, current string, logger *slog.Logger) *Checker {
	return &Checker{DB: db, Current: current, URL: LatestURL, ListURL: ListURL, HTTP: &http.Client{Timeout: 20 * time.Second}, Logger: logger}
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

// fetch lit la derniere version : la derniere stable, ou, depuis une
// version de test, la plus recente de toutes, test compris.
func (c *Checker) fetch(ctx context.Context) (Release, error) {
	testing := false
	if v, ok := parse(c.Current); ok && v.pre != "" {
		testing = true
	}
	url := c.URL
	if testing {
		url = c.ListURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
	type entry struct {
		Tag   string `json:"tag_name"`
		Draft bool   `json:"draft"`
	}
	var tags []string
	body := io.LimitReader(resp.Body, maxBody)
	if testing {
		var list []entry
		if err := json.NewDecoder(body).Decode(&list); err != nil {
			return Release{}, errors.New("réponse de GitHub illisible")
		}
		for _, e := range list {
			if !e.Draft {
				tags = append(tags, e.Tag)
			}
		}
	} else {
		var one entry
		if err := json.NewDecoder(body).Decode(&one); err != nil {
			return Release{}, errors.New("réponse de GitHub illisible")
		}
		tags = append(tags, one.Tag)
	}
	best := ""
	for _, t := range tags {
		if v, ok := parse(t); ok && (best == "" || Newer(v.String(), best)) {
			best = v.String()
		}
	}
	if best == "" {
		return Release{}, errors.New("numéro de version illisible")
	}
	// L'adresse se construit ici : rien de la reponse ne devient un lien.
	return Release{Version: best, URL: ReleasesURL + "/tag/v" + best}, nil
}

type version struct {
	core [3]int
	pre  string
}

func (v version) String() string {
	s := strconv.Itoa(v.core[0]) + "." + strconv.Itoa(v.core[1]) + "." + strconv.Itoa(v.core[2])
	if v.pre != "" {
		s += "-" + v.pre
	}
	return s
}

// Newer dit si la version a est plus recente que b. Une version de test
// precede sa version finale : 1.9.0-rc1, 1.9.0-rc2, puis 1.9.0. Une version
// illisible, comme une version de developpement, n'est jamais comparee.
func Newer(a, b string) bool {
	va, okA := parse(a)
	vb, okB := parse(b)
	if !okA || !okB {
		return false
	}
	for i := range va.core {
		if va.core[i] != vb.core[i] {
			return va.core[i] > vb.core[i]
		}
	}
	switch {
	case va.pre == vb.pre:
		return false
	case va.pre == "":
		return true
	case vb.pre == "":
		return false
	}
	return natural(va.pre, vb.pre) > 0
}

// natural compare deux suffixes de test, les nombres comme des nombres :
// rc2 avant rc10.
func natural(a, b string) int {
	for a != "" && b != "" {
		ca, ra := chunk(a)
		cb, rb := chunk(b)
		na, errA := strconv.Atoi(ca)
		nb, errB := strconv.Atoi(cb)
		switch {
		case errA == nil && errB == nil && na != nb:
			if na < nb {
				return -1
			}
			return 1
		case (errA != nil || errB != nil) && ca != cb:
			if ca < cb {
				return -1
			}
			return 1
		}
		a, b = ra, rb
	}
	return len(a) - len(b)
}

// chunk coupe le debut d'un suffixe : une suite de chiffres, ou d'autres
// caracteres.
func chunk(s string) (string, string) {
	digit := s[0] >= '0' && s[0] <= '9'
	i := 1
	for i < len(s) && (s[i] >= '0' && s[i] <= '9') == digit {
		i++
	}
	return s[:i], s[i:]
}

// Comparable dit si une version se compare aux versions publiees : une
// version de developpement ne se compare a rien.
func Comparable(v string) bool {
	_, ok := parse(v)
	return ok
}

func parse(v string) (version, bool) {
	m := semver.FindStringSubmatch(v)
	if m == nil {
		return version{}, false
	}
	var out version
	for i := range out.core {
		out.core[i], _ = strconv.Atoi(m[i+1])
	}
	out.pre = m[4]
	return out, true
}
