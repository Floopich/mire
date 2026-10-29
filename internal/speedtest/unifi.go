package speedtest

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/floopich/mire/internal/store"
)

// Une passerelle UniFi (Cloud Gateway, Dream Machine) ne rend que le dernier
// test de chaque acces Internet : Mire le releve a chaque lecture. Acces par
// une cle API de UniFi Network, ou par un compte local de la console.

// dashboardPath est le tableau de bord de UniFi Network, qui porte le
// dernier test ; une heure d'historique suffit, le reste est ignore.
const dashboardPath = "/proxy/network/v2/api/site/default/aggregated-dashboard?historySeconds=3600"

// UniFi interroge une passerelle UniFi.
type UniFi struct {
	URL            string
	Key            string
	User, Password string
	HTTP           *http.Client

	mu       sync.Mutex
	loggedIn bool
}

// NewUniFi prepare un client. Le certificat d'une passerelle est autosigne :
// il n'est accepte que pour une adresse du reseau local.
func NewUniFi(address, key, user, password string) *UniFi {
	address = strings.TrimRight(strings.TrimSpace(address), "/")
	if rest, ok := strings.CutPrefix(address, "http://"); ok {
		address = "https://" + rest
	}
	jar, _ := cookiejar.New(nil)
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if u, err := url.Parse(address); err == nil && local(u.Hostname()) {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12} //nolint:gosec // certificat autosigne de la passerelle, reseau local seulement
	}
	return &UniFi{URL: address, Key: strings.TrimSpace(key), User: strings.TrimSpace(user), Password: password,
		HTTP: &http.Client{Timeout: 20 * time.Second, Transport: tr, Jar: jar}}
}

// local dit si l'hote est sur le reseau local : adresse privee ou nom sans
// domaine public.
func local(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()
	}
	return !strings.Contains(host, ".") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".lan") ||
		strings.HasSuffix(host, ".home.arpa")
}

// unreachable explique une passerelle muette : le plus souvent, un port
// ajoute a son adresse (celui de Speedtest Tracker, par exemple).
func (u *UniFi) unreachable() error {
	if p, err := url.Parse(u.URL); err == nil && p.Port() != "" && p.Port() != "443" {
		return fmt.Errorf("la passerelle ne répond pas à %s : indiquez son adresse seule, sans port, par exemple https://%s", u.URL, p.Hostname())
	}
	return fmt.Errorf("la passerelle ne répond pas à %s", u.URL)
}

// errAuth signale un acces refuse.
var errAuth = errors.New("accès refusé")

func (u *UniFi) login(ctx context.Context) error {
	if u.User == "" || u.Password == "" {
		return errors.New("clé API ou compte local de la passerelle non renseigné")
	}
	body, _ := json.Marshal(map[string]string{"username": u.User, "password": u.Password})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.URL+"/api/auth/login", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("adresse de la passerelle invalide : %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := u.HTTP.Do(req)
	if err != nil {
		return u.unreachable()
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	switch {
	case resp.StatusCode == http.StatusOK:
		u.loggedIn = true
		return nil
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return errors.New("la passerelle refuse l'identifiant ou le mot de passe : il faut un compte local de la console, sans double authentification")
	case resp.StatusCode == http.StatusTooManyRequests:
		return errors.New("la passerelle bloque les connexions après trop d'essais : réessayez dans quelques minutes")
	default:
		return fmt.Errorf("la passerelle a refusé la connexion : HTTP %d", resp.StatusCode)
	}
}

func (u *UniFi) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.URL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("adresse de la passerelle invalide : %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if u.Key != "" {
		req.Header.Set("X-API-KEY", u.Key)
	}
	resp, err := u.HTTP.Do(req)
	if err != nil {
		return nil, u.unreachable()
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return body, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, errAuth
	case http.StatusNotFound:
		return nil, errors.New("UniFi Network ne répond pas à cette adresse : celle de la passerelle, et UniFi Network 11 ou plus récent, sont nécessaires")
	default:
		return nil, fmt.Errorf("la passerelle a répondu HTTP %d", resp.StatusCode)
	}
}

// dashboard lit le tableau de bord, en se connectant si besoin.
func (u *UniFi) dashboard(ctx context.Context) ([]byte, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.Key == "" && !u.loggedIn {
		if err := u.login(ctx); err != nil {
			return nil, err
		}
	}
	body, err := u.get(ctx, dashboardPath)
	if errors.Is(err, errAuth) && u.Key == "" {
		// La session a expire : une seule reconnexion.
		u.loggedIn = false
		if err := u.login(ctx); err != nil {
			return nil, err
		}
		body, err = u.get(ctx, dashboardPath)
	}
	if errors.Is(err, errAuth) {
		if u.Key != "" {
			return nil, errors.New("la passerelle refuse la clé API")
		}
		return nil, errors.New("ce compte n'a pas accès à UniFi Network")
	}
	return body, err
}

// wanTest est le dernier test d'un acces Internet.
type wanTest struct {
	Download float64 `json:"download_mbps"`
	Upload   float64 `json:"upload_mbps"`
	Latency  float64 `json:"latency_ms"`
	Time     int64   `json:"time"` // millisecondes
	Group    string  `json:"wan_networkgroup"`
	Iface    string  `json:"interface_name"`
}

// latest choisit le test a retenir : celui de l'acces principal (WAN), a
// defaut le plus recent. Un acces secondaire ne doit pas
// passer pour la ligne.
func latest(body []byte) (store.SpeedTest, bool, error) {
	var r struct {
		SpeedTest *struct {
			Supported bool      `json:"is_supported"`
			Data      []wanTest `json:"data"`
		} `json:"speed_test"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return store.SpeedTest{}, false, errors.New("réponse illisible : est-ce bien une passerelle UniFi à cette adresse ?")
	}
	if r.SpeedTest == nil || !r.SpeedTest.Supported {
		return store.SpeedTest{}, false, errors.New("cette passerelle ne fait pas de test de débit")
	}
	var best *wanTest
	for i := range r.SpeedTest.Data {
		t := &r.SpeedTest.Data[i]
		if t.Time <= 0 || t.Download <= 0 {
			continue
		}
		switch {
		case best == nil,
			t.Group == "WAN" && best.Group != "WAN",
			(t.Group == "WAN") == (best.Group == "WAN") && t.Time > best.Time:
			best = t
		}
	}
	if best == nil {
		return store.SpeedTest{}, false, nil
	}
	ping := best.Latency
	server := "Passerelle UniFi"
	if best.Group != "" {
		server += ", " + best.Group
	}
	return store.SpeedTest{Time: time.UnixMilli(best.Time).UTC().Truncate(time.Second), Download: best.Download,
		Upload: best.Upload, Ping: &ping, Server: server}, true, nil
}

// Since rend le dernier test s'il est posterieur a since.
func (u *UniFi) Since(ctx context.Context, since time.Time, _ int) ([]store.SpeedTest, error) {
	if u.URL == "" {
		return nil, errors.New("adresse de la passerelle non renseignée")
	}
	body, err := u.dashboard(ctx)
	if err != nil {
		return nil, err
	}
	t, ok, err := latest(body)
	if err != nil || !ok || !t.Time.After(since) {
		return nil, err
	}
	return []store.SpeedTest{t}, nil
}

// Schedule dit si les tests de debit automatiques de la passerelle sont
// actifs, et leur programme (expression cron). C'est la cause habituelle
// d'un debit qui ne bouge plus : la passerelle ne teste que sur demande.
func (u *UniFi) Schedule(ctx context.Context) (enabled bool, cron string, err error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.Key == "" && !u.loggedIn {
		if err := u.login(ctx); err != nil {
			return false, "", err
		}
	}
	body, err := u.get(ctx, "/proxy/network/api/s/default/get/setting")
	if err != nil {
		return false, "", err
	}
	var r struct {
		Data []struct {
			Key      string `json:"key"`
			Enabled  bool   `json:"enabled"`
			CronExpr string `json:"cron_expr"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return false, "", err
	}
	for _, s := range r.Data {
		if s.Key == "auto_speedtest" {
			return s.Enabled, s.CronExpr, nil
		}
	}
	return false, "", errors.New("réglage des tests automatiques introuvable")
}
