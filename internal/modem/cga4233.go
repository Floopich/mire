// Package modem parle aux modems cable.
package modem

import (
	"context"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/floopich/mire/internal/docsis"
)

// docsisPath porte les cinq tables en une seule requete.
//
// Les demander separement multiplierait par cinq le cout d'une collecte sur un
// firmware qui met deja plusieurs secondes a repondre.
const docsisPath = "/api/v1/modem/exUSTbl,exDSTbl,USTbl,DSTbl,ErrTbl"

const (
	// Le firmware met environ neuf secondes a assembler les tables DOCSIS.
	// Un delai de dix secondes faisait echouer des collectes par intermittence.
	readTimeout = 30 * time.Second
	// La phase d'authentification, elle, repond vite.
	authTimeout = 10 * time.Second

	pbkdf2Iterations = 1000
	pbkdf2Length     = 16
)

// ErrAuth signale un refus d'authentification, distinct d'une panne reseau :
// l'un se corrige en changeant les identifiants, l'autre en attendant.
var ErrAuth = errors.New("authentification refusée par le modem")

// CGA4233 pilote le Technicolor CGA4233 en firmware VOO.
//
// Le modem n'accorde qu'une session a la fois : un meme pilote sert la
// collecte et la page Modem, et ses appels passent un par un.
type CGA4233 struct {
	mu       sync.Mutex
	baseURL  string
	user     string
	password string
	client   *http.Client
	token    string
}

// NewCGA4233 prepare un pilote. Aucune connexion n'est etablie ici.
func NewCGA4233(baseURL, user, password string) (*CGA4233, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmed == "" {
		return nil, errors.New("adresse du modem vide")
	}
	if _, err := url.Parse(trimmed); err != nil {
		return nil, fmt.Errorf("adresse du modem invalide : %w", err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &CGA4233{
		baseURL:  trimmed,
		user:     user,
		password: password,
		client:   &http.Client{Jar: jar, Timeout: readTimeout},
	}, nil
}

// Snapshot renvoie un releve brut, en se reconnectant si la session a expire.
func (m *CGA4233) Snapshot(ctx context.Context) (docsis.RawSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	body, err := m.fetch(ctx, docsisPath)
	if err != nil {
		return docsis.RawSnapshot{}, err
	}
	return parsePayload(body)
}

// fetch interroge un endpoint authentifie, avec une seule reconnexion.
//
// Seuls 400, 401 et 403 justifient de rejouer le cycle de login complet : sur
// les autres echecs, insister brulerait une requete et une session sur un
// modem qui n'en accorde pas beaucoup.
func (m *CGA4233) fetch(ctx context.Context, path string) ([]byte, error) {
	if m.token == "" {
		if err := m.login(ctx); err != nil {
			return nil, err
		}
	}
	body, status, err := m.authenticatedGet(ctx, path)
	if err == nil {
		return body, nil
	}
	if status != http.StatusBadRequest && status != http.StatusUnauthorized && status != http.StatusForbidden {
		return nil, err
	}

	m.invalidate()
	if err := m.login(ctx); err != nil {
		return nil, err
	}
	body, _, err = m.authenticatedGet(ctx, path)
	if err != nil {
		m.invalidate()
		return nil, fmt.Errorf("%s après reconnexion : %w", path, err)
	}
	return body, nil
}

func (m *CGA4233) authenticatedGet(ctx context.Context, path string) ([]byte, int, error) {
	// Le parametre "_" est un anti-cache : le firmware sert sinon une reponse
	// figee, ce qui donnerait un historique de releves identiques.
	full := fmt.Sprintf("%s%s?_=%d", m.baseURL, path, time.Now().UnixMilli())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return nil, 0, err
	}
	m.applyHeaders(req)
	if m.token != "" {
		req.Header.Set("X-CSRF-TOKEN", m.token)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%s : %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("%s : lecture : %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("%s : HTTP %d", path, resp.StatusCode)
	}
	return body, resp.StatusCode, nil
}

// applyHeaders pose les en-tetes que le firmware verifie sur chaque requete.
func (m *CGA4233) applyHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Referer", m.baseURL+"/")
}

// login joue la sequence en deux temps du firmware.
//
// Premiere requete : le mot de passe litteral "seeksalthash" demande les deux
// sels. Deuxieme : le mot de passe derive deux fois les rend. Le mot de passe
// de l'abonne ne transite donc jamais en clair.
func (m *CGA4233) login(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, authTimeout)
	defer cancel()

	m.setCookie("cwd", "No")

	var salts struct {
		Salt    string `json:"salt"`
		SaltWeb string `json:"saltwebui"`
	}
	if err := m.postLogin(ctx, "seeksalthash", &salts); err != nil {
		return err
	}
	if salts.Salt == "" || salts.SaltWeb == "" {
		return fmt.Errorf("%w : le modem n'a pas renvoyé de sel", ErrAuth)
	}

	derived, err := deriveTwice(m.password, salts.Salt, salts.SaltWeb)
	if err != nil {
		return err
	}

	var result struct {
		Error string `json:"error"`
		Token string `json:"token"`
	}
	if err := m.postLogin(ctx, derived, &result); err != nil {
		return err
	}
	if result.Error != "" && result.Error != "ok" {
		return fmt.Errorf("%w : %s", ErrAuth, result.Error)
	}

	m.token = result.Token
	if m.token == "" {
		// Ce firmware ne renvoie pas de champ "token" : l'interface web relit
		// la valeur du cookie "auth" et la rejoue en en-tete. Sans elle, les
		// endpoints modem/* repondent 401.
		m.token = m.cookie("auth")
	}
	if m.token == "" {
		return fmt.Errorf("%w : aucun jeton dans la réponse ni dans le cookie auth", ErrAuth)
	}

	// Initialisation du menu : le firmware l'attend, mais son echec n'empeche
	// pas la lecture des tables.
	_, _, _ = m.authenticatedGet(ctx, "/api/v1/session/menu")
	return nil
}

func (m *CGA4233) postLogin(ctx context.Context, password string, out any) error {
	form := url.Values{
		"username": {m.user},
		"password": {password},
		"logout":   {"true"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		m.baseURL+"/api/v1/session/login", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	m.applyHeaders(req)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")

	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("connexion au modem : %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("connexion au modem : lecture : %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w : HTTP %d", ErrAuth, resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%w : réponse illisible : %s", ErrAuth, truncate(body))
	}
	return nil
}

// deriveTwice reproduit la derivation attendue par le firmware.
//
// Le deuxieme tour prend la representation hexadecimale du premier, pas ses
// octets : c'est ce que fait l'interface web, et s'en ecarter donne un hash
// syntaxiquement valide que le modem refuse.
func deriveTwice(password, salt, saltWeb string) (string, error) {
	first, err := pbkdf2.Key(sha256.New, password, []byte(salt), pbkdf2Iterations, pbkdf2Length)
	if err != nil {
		return "", err
	}
	firstHex := hex.EncodeToString(first)
	second, err := pbkdf2.Key(sha256.New, firstHex, []byte(saltWeb), pbkdf2Iterations, pbkdf2Length)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(second), nil
}

func (m *CGA4233) invalidate() {
	m.token = ""
	if jar, err := cookiejar.New(nil); err == nil {
		m.client.Jar = jar
	}
}

func (m *CGA4233) setCookie(name, value string) {
	u, err := url.Parse(m.baseURL)
	if err != nil {
		return
	}
	m.client.Jar.SetCookies(u, []*http.Cookie{{Name: name, Value: value}})
}

func (m *CGA4233) cookie(name string) string {
	u, err := url.Parse(m.baseURL)
	if err != nil {
		return ""
	}
	for _, c := range m.client.Jar.Cookies(u) {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

func truncate(b []byte) string {
	const max = 120
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		return s[:max] + "..."
	}
	return strconv.Quote(s)
}
