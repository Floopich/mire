package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/settings"
)

// timeout borne chaque envoi : un service lent ne doit pas retenir le journal.
const timeout = 15 * time.Second

// New construit l'expediteur du service choisi dans les reglages.
func New(v settings.Values) (Sender, error) {
	need := func(key, what string) (string, error) {
		s := strings.TrimSpace(v.String(key))
		if s == "" {
			return "", fmt.Errorf("%s non renseigné", what)
		}
		return s, nil
	}
	client := &http.Client{Timeout: timeout}
	switch service := v.String(settings.NotifyService); service {
	case "ntfy":
		topic, err := need(settings.NtfyTopic, "sujet ntfy")
		if err != nil {
			return nil, err
		}
		server := strings.TrimSpace(v.String(settings.NtfyURL))
		if server == "" {
			server = "https://ntfy.sh"
		}
		return &ntfy{client: client, server: server, topic: topic, token: v.String(settings.NtfyToken)}, nil
	case "gotify":
		server, err := need(settings.GotifyURL, "serveur Gotify")
		if err != nil {
			return nil, err
		}
		token, err := need(settings.GotifyToken, "jeton Gotify")
		if err != nil {
			return nil, err
		}
		return &gotify{client: client, server: server, token: token}, nil
	case "discord":
		hook, err := need(settings.DiscordURL, "webhook Discord")
		if err != nil {
			return nil, err
		}
		return &discord{client: client, url: hook}, nil
	case "telegram":
		token, err := need(settings.TelegramToken, "jeton du bot Telegram")
		if err != nil {
			return nil, err
		}
		chat, err := need(settings.TelegramChat, "conversation Telegram")
		if err != nil {
			return nil, err
		}
		return &telegram{client: client, api: "https://api.telegram.org", token: token, chat: chat}, nil
	case "slack":
		hook, err := need(settings.SlackURL, "webhook Slack")
		if err != nil {
			return nil, err
		}
		return &slack{client: client, url: hook}, nil
	case "mail":
		server, err := need(settings.MailServer, "serveur d'envoi")
		if err != nil {
			return nil, err
		}
		from, err := need(settings.MailFrom, "expéditeur")
		if err != nil {
			return nil, err
		}
		to, err := need(settings.MailTo, "destinataire")
		if err != nil {
			return nil, err
		}
		var rcpt []string
		for _, a := range strings.Split(to, ",") {
			if a = strings.TrimSpace(a); a != "" {
				rcpt = append(rcpt, a)
			}
		}
		return &mailer{server: server, security: v.String(settings.MailSecurity),
			user: v.String(settings.MailUser), password: v.String(settings.MailPassword), from: from, to: rcpt}, nil
	case "webhook":
		hook, err := need(settings.WebhookURL, "adresse du webhook")
		if err != nil {
			return nil, err
		}
		return &webhook{client: client, url: hook}, nil
	default:
		return nil, fmt.Errorf("service de notification inconnu : %q", service)
	}
}

// post envoie un document JSON. Les adresses de webhook et de bot contiennent
// un secret : une erreur ne reprend donc jamais l'adresse.
func post(ctx context.Context, c *http.Client, service, target string, header http.Header, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%s : adresse invalide", service)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mire")
	for k, vs := range header {
		req.Header[k] = vs
	}
	resp, err := c.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return fmt.Errorf("%s injoignable : %v", service, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return statusError(service, resp.StatusCode)
	}
	return nil
}

// text met le message en texte brut : titre, corps, lien.
func text(m Message) string {
	s := m.Title + "\n" + m.Body
	if m.Link != "" {
		s += "\n" + m.Link
	}
	return s
}

type ntfy struct {
	client               *http.Client
	server, topic, token string
}

func (n *ntfy) Send(ctx context.Context, m Message) error {
	// Publication JSON plutot qu'en-tetes : le titre garde ses accents.
	payload := map[string]any{
		"topic":    n.topic,
		"title":    m.Title,
		"message":  m.Body,
		"priority": map[analyze.Level]int{analyze.Critical: 4, analyze.Warning: 3}[m.Level],
		"tags":     []string{map[analyze.Level]string{analyze.Critical: "rotating_light", analyze.Warning: "warning", analyze.Good: "white_check_mark", analyze.Tolerated: "white_check_mark"}[m.Level]},
	}
	if payload["priority"] == 0 {
		payload["priority"] = 2
	}
	if m.Link != "" {
		payload["click"] = m.Link
	}
	h := http.Header{}
	if n.token != "" {
		h.Set("Authorization", "Bearer "+n.token)
	}
	return post(ctx, n.client, "ntfy", strings.TrimRight(n.server, "/"), h, payload)
}

type gotify struct {
	client        *http.Client
	server, token string
}

func (g *gotify) Send(ctx context.Context, m Message) error {
	priority := map[analyze.Level]int{analyze.Critical: 8, analyze.Warning: 5}[m.Level]
	if priority == 0 {
		priority = 2
	}
	payload := map[string]any{"title": m.Title, "message": m.Body, "priority": priority}
	if m.Link != "" {
		payload["extras"] = map[string]any{"client::notification": map[string]any{"click": map[string]string{"url": m.Link}}}
	}
	h := http.Header{}
	h.Set("X-Gotify-Key", g.token)
	return post(ctx, g.client, "Gotify", strings.TrimRight(g.server, "/")+"/message", h, payload)
}

// Couleurs des niveaux, celles de la charte, pour les services qui en
// affichent une : Discord et Slack.
var levelColor = map[analyze.Level]int{
	analyze.Good: 0x1B6D24, analyze.Tolerated: 0x005EB5, analyze.Warning: 0x7B5800, analyze.Critical: 0xB91D20,
}

type discord struct {
	client *http.Client
	url    string
}

func (d *discord) Send(ctx context.Context, m Message) error {
	embed := map[string]any{"title": m.Title, "description": m.Body, "color": levelColor[m.Level]}
	if m.Link != "" {
		embed["url"] = m.Link
	}
	if !m.Time.IsZero() {
		embed["timestamp"] = m.Time.UTC().Format(time.RFC3339)
	}
	return post(ctx, d.client, "Discord", d.url, nil, map[string]any{"username": "Mire", "embeds": []any{embed}})
}

type telegram struct {
	client           *http.Client
	api, token, chat string
}

func (t *telegram) Send(ctx context.Context, m Message) error {
	return post(ctx, t.client, "Telegram", t.api+"/bot"+t.token+"/sendMessage", nil, map[string]any{
		"chat_id": t.chat, "text": text(m), "disable_web_page_preview": true,
	})
}

type slack struct {
	client *http.Client
	url    string
}

func (s *slack) Send(ctx context.Context, m Message) error {
	msg := "*" + m.Title + "*\n" + m.Body
	if m.Link != "" {
		msg += "\n<" + m.Link + "|Ouvrir le journal>"
	}
	return post(ctx, s.client, "Slack", s.url, nil, map[string]any{"text": msg})
}

type webhook struct {
	client *http.Client
	url    string
}

func (w *webhook) Send(ctx context.Context, m Message) error {
	payload := map[string]any{
		"titre": m.Title, "message": m.Body, "niveau": tag(m.Level),
		"evenements": m.Kinds, "heure": m.Time.UTC().Format(time.RFC3339),
	}
	if m.Link != "" {
		payload["lien"] = m.Link
	}
	return post(ctx, w.client, "Le webhook", w.url, nil, payload)
}

type mailer struct {
	server, security, user, password, from string
	to                                     []string
}

func (s *mailer) Send(ctx context.Context, m Message) error {
	host, _, err := net.SplitHostPort(s.server)
	if err != nil {
		return fmt.Errorf("serveur d'envoi %q : forme attendue serveur:port", s.server)
	}
	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	dialer := &net.Dialer{Deadline: deadline}
	tlsConfig := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}

	var conn net.Conn
	if s.security == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", s.server, tlsConfig)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", s.server)
	}
	if err != nil {
		return fmt.Errorf("serveur d'envoi injoignable : %w", err)
	}
	_ = conn.SetDeadline(deadline)
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("serveur d'envoi : %w", err)
	}
	defer func() { _ = c.Close() }()

	if s.security == "" || s.security == "starttls" {
		// STARTTLS est exige : sans lui, le mot de passe passerait en clair.
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("le serveur d'envoi ne propose pas STARTTLS : choisissez TLS, ou un autre port")
		}
		if err := c.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("STARTTLS : %w", err)
		}
	}
	if s.user != "" {
		if err := c.Auth(smtp.PlainAuth("", s.user, s.password, host)); err != nil {
			return errors.New("le serveur d'envoi refuse l'identifiant ou le mot de passe")
		}
	}
	if err := c.Mail(s.from); err != nil {
		return fmt.Errorf("expéditeur refusé : %w", err)
	}
	for _, r := range s.to {
		if err := c.Rcpt(r); err != nil {
			return fmt.Errorf("destinataire %s refusé : %w", r, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(s.compose(m)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// compose ecrit le message : texte brut UTF-8, en quoted-printable pour
// traverser n'importe quel serveur.
func (s *mailer) compose(m Message) []byte {
	var b bytes.Buffer
	when := m.Time
	if when.IsZero() {
		when = time.Now()
	}
	fmt.Fprintf(&b, "From: %s\r\n", s.from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(s.to, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", m.Title))
	fmt.Fprintf(&b, "Date: %s\r\n", when.Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	qp := quotedprintable.NewWriter(&b)
	body := m.Body
	if m.Link != "" {
		body += "\n\n" + m.Link
	}
	_, _ = qp.Write([]byte(strings.ReplaceAll(body, "\n", "\r\n") + "\r\n"))
	_ = qp.Close()
	return b.Bytes()
}
