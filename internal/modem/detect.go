package modem

import (
	"bufio"
	"context"
	"html"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Candidate est un appareil qui repond a l'une des adresses essayees.
type Candidate struct {
	URL string
	// Title est le titre de la page d'accueil, souvent le nom du modem.
	Title string
	// Model est le modele reconnu, vide si l'appareil n'est pas identifie.
	Model *Model
	// DOCSIS dit si la page parle de DOCSIS : un modem cable, meme inconnu.
	DOCSIS bool
}

// DefaultHosts sont les adresses ou l'on trouve un modem cable.
//
// 192.168.100.1 est l'adresse de diagnostic imposee par la specification
// DOCSIS, joignable en bridge comme en routeur ; les deux autres sont les
// adresses d'administration les plus courantes en Belgique et en France.
var DefaultHosts = []string{"192.168.100.1", "192.168.0.1", "192.168.1.1"}

const probeTimeout = 2 * time.Second

var titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// Detect essaie chaque adresse et renvoie les appareils qui repondent.
//
// Aucun identifiant n'est envoye : seule la page d'accueil est lue, ce qui
// suffit a reconnaitre la plupart des firmwares. Les adresses sont essayees en
// parallele, en HTTP puis en HTTPS.
func Detect(ctx context.Context, hosts []string) []Candidate {
	// Beaucoup de modems renvoient la page d'accueil vers leur page de
	// connexion : le client suit, sans quitter l'appareil interroge.
	client := publicClient(probeTimeout)

	var (
		mu    sync.Mutex
		found []Candidate
		wg    sync.WaitGroup
	)
	for _, host := range hosts {
		wg.Add(1)
		go func(host string) {
			defer wg.Done()
			if c, ok := probe(ctx, client, host); ok {
				mu.Lock()
				found = append(found, c)
				mu.Unlock()
			}
		}(host)
	}
	wg.Wait()

	// Un modele reconnu d'abord, puis un modem cable inconnu, puis le reste ;
	// a egalite, l'ordre des adresses essayees.
	rank := func(c Candidate) int {
		switch {
		case c.Model != nil:
			return 0
		case c.DOCSIS:
			return 1
		default:
			return 2
		}
	}
	order := map[string]int{}
	for i, h := range hosts {
		order[h] = i
	}
	sort.SliceStable(found, func(i, j int) bool {
		if ri, rj := rank(found[i]), rank(found[j]); ri != rj {
			return ri < rj
		}
		return order[hostOf(found[i].URL)] < order[hostOf(found[j].URL)]
	})
	return found
}

func probe(ctx context.Context, client *http.Client, host string) (Candidate, bool) {
	for _, scheme := range []string{"http", "https"} {
		url := scheme + "://" + host
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return Candidate{}, false
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
		return identify(url, string(body)), true
	}
	return Candidate{}, false
}

// identify reconnait un modele d'apres sa page d'accueil.
func identify(url, body string) Candidate {
	c := Candidate{URL: url}
	if m := titleRe.FindStringSubmatch(body); m != nil {
		c.Title = strings.Join(strings.Fields(html.UnescapeString(m[1])), " ")
	}
	lower := strings.ToLower(body)
	c.DOCSIS = strings.Contains(lower, "docsis")
	for _, m := range Models() {
		for _, marker := range m.Markers {
			if strings.Contains(lower, marker) {
				model := m
				c.Model = &model
				return c
			}
		}
	}
	return c
}

func hostOf(url string) string {
	_, rest, _ := strings.Cut(url, "://")
	return rest
}

// Hosts compose la liste des adresses a essayer : la passerelle de la
// machine en tete, puis les adresses usuelles, sans doublon.
func Hosts(extra ...string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(h string) {
		h = strings.TrimSpace(h)
		h = strings.TrimPrefix(strings.TrimPrefix(h, "http://"), "https://")
		h = strings.TrimSuffix(h, "/")
		if h != "" && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	for _, h := range extra {
		add(h)
	}
	if gw := DefaultGateway(); gw != "" {
		add(gw)
	}
	for _, h := range DefaultHosts {
		add(h)
	}
	return out
}

// DefaultGateway lit la passerelle par defaut de Linux, vide ailleurs.
func DefaultGateway() string {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	return parseRoute(f)
}

// parseRoute extrait la passerelle de la route par defaut (destination 0).
func parseRoute(r io.Reader) string {
	s := bufio.NewScanner(r)
	s.Scan() // en-tete
	for s.Scan() {
		f := strings.Fields(s.Text())
		if len(f) < 3 || f[1] != "00000000" {
			continue
		}
		v, err := strconv.ParseUint(f[2], 16, 32)
		if err != nil || v == 0 {
			continue
		}
		// /proc/net/route ecrit l'adresse en petit-boutiste.
		ip := net.IPv4(byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
		return ip.String()
	}
	return ""
}
