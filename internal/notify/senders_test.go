package notify

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/settings"
)

// requete est ce qu'un service a recu.
type requete struct {
	path   string
	header http.Header
	body   map[string]any
}

func serveur(t *testing.T, status int) (*httptest.Server, chan requete) {
	t.Helper()
	recu := make(chan requete, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		recu <- requete{path: r.URL.Path, header: r.Header, body: body}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, recu
}

func message() Message {
	return Message{Title: "Mire : alerte critique", Body: "Coupure d'Internet.", Level: analyze.Critical,
		Link: "https://mire.exemple.be/evenements", Time: time.Date(2026, 10, 26, 19, 30, 0, 0, time.UTC),
		Kinds: []string{"coupure"}}
}

func TestChaqueServiceRecoitSonFormat(t *testing.T) {
	ctx := context.Background()
	client := &http.Client{Timeout: 5 * time.Second}

	t.Run("ntfy", func(t *testing.T) {
		srv, recu := serveur(t, http.StatusOK)
		if err := (&ntfy{client: client, server: srv.URL, topic: "ma-ligne", token: "tk"}).Send(ctx, message()); err != nil {
			t.Fatal(err)
		}
		r := <-recu
		if r.body["topic"] != "ma-ligne" || r.body["title"] != "Mire : alerte critique" || r.body["click"] == nil {
			t.Errorf("corps %v", r.body)
		}
		if r.body["priority"].(float64) != 4 || r.header.Get("Authorization") != "Bearer tk" {
			t.Errorf("priorite %v, autorisation %q", r.body["priority"], r.header.Get("Authorization"))
		}
	})
	t.Run("gotify", func(t *testing.T) {
		srv, recu := serveur(t, http.StatusOK)
		if err := (&gotify{client: client, server: srv.URL + "/", token: "app"}).Send(ctx, message()); err != nil {
			t.Fatal(err)
		}
		r := <-recu
		if r.path != "/message" || r.header.Get("X-Gotify-Key") != "app" || r.body["priority"].(float64) != 8 {
			t.Errorf("%s %v %v", r.path, r.header, r.body)
		}
	})
	t.Run("discord", func(t *testing.T) {
		srv, recu := serveur(t, http.StatusNoContent)
		if err := (&discord{client: client, url: srv.URL}).Send(ctx, message()); err != nil {
			t.Fatal(err)
		}
		embed := (<-recu).body["embeds"].([]any)[0].(map[string]any)
		if embed["title"] != "Mire : alerte critique" || embed["color"].(float64) != 0xB91D20 {
			t.Errorf("embed %v", embed)
		}
	})
	t.Run("telegram", func(t *testing.T) {
		srv, recu := serveur(t, http.StatusOK)
		if err := (&telegram{client: client, api: srv.URL, token: "123:abc", chat: "42"}).Send(ctx, message()); err != nil {
			t.Fatal(err)
		}
		r := <-recu
		if r.path != "/bot123:abc/sendMessage" || r.body["chat_id"] != "42" ||
			!strings.Contains(r.body["text"].(string), "Coupure d'Internet.") {
			t.Errorf("%s %v", r.path, r.body)
		}
	})
	t.Run("slack", func(t *testing.T) {
		srv, recu := serveur(t, http.StatusOK)
		if err := (&slack{client: client, url: srv.URL}).Send(ctx, message()); err != nil {
			t.Fatal(err)
		}
		if txt := (<-recu).body["text"].(string); !strings.HasPrefix(txt, "*Mire : alerte critique*") || !strings.Contains(txt, "|Ouvrir le journal>") {
			t.Errorf("texte %q", txt)
		}
	})
	t.Run("webhook", func(t *testing.T) {
		srv, recu := serveur(t, http.StatusOK)
		if err := (&webhook{client: client, url: srv.URL}).Send(ctx, message()); err != nil {
			t.Fatal(err)
		}
		b := (<-recu).body
		if b["niveau"] != "critique" || b["heure"] != "2026-10-26T19:30:00Z" || b["lien"] == nil {
			t.Errorf("corps %v", b)
		}
	})
}

func TestUneErreurNeReveleAucunSecret(t *testing.T) {
	ctx := context.Background()
	client := &http.Client{Timeout: 2 * time.Second}

	srv, recu := serveur(t, http.StatusUnauthorized)
	err := (&telegram{client: client, api: srv.URL, token: "123:secret", chat: "42"}).Send(ctx, message())
	<-recu
	if err == nil || strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "401") {
		t.Errorf("refus : %v", err)
	}

	// Serveur ferme : l'erreur de transport du client HTTP contient l'adresse,
	// qu'il faut retirer.
	ferme := httptest.NewServer(http.NotFoundHandler())
	ferme.Close()
	err = (&discord{client: client, url: ferme.URL + "/api/webhooks/1/jeton-secret"}).Send(ctx, message())
	if err == nil || strings.Contains(err.Error(), "jeton-secret") {
		t.Errorf("transport : %v", err)
	}
}

func TestReglagesIncomplets(t *testing.T) {
	cas := map[string]map[string]string{
		"ntfy":     {settings.NotifyService: "ntfy"},
		"gotify":   {settings.NotifyService: "gotify", settings.GotifyURL: "https://g.exemple.be"},
		"telegram": {settings.NotifyService: "telegram", settings.TelegramToken: "1:a"},
		"mail":     {settings.NotifyService: "mail", settings.MailServer: "smtp.exemple.be:587", settings.MailFrom: "a@b.be"},
		"inconnu":  {settings.NotifyService: "pigeon"},
	}
	for nom, valeurs := range cas {
		if _, err := New(settings.Fixed(valeurs)); err == nil {
			t.Errorf("%s : erreur attendue", nom)
		}
	}
	s, err := New(settings.Fixed(map[string]string{settings.NotifyService: "ntfy", settings.NtfyTopic: "sujet"}))
	if err != nil {
		t.Fatal(err)
	}
	if s.(*ntfy).server != "https://ntfy.sh" {
		t.Errorf("serveur ntfy par defaut : %q", s.(*ntfy).server)
	}
}

// smtpFactice joue un serveur d'envoi minimal et rend le message recu.
func smtpFactice(t *testing.T, starttls bool) (string, chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	recu := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		r := bufio.NewReader(conn)
		say := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
		say("220 factice")
		var data strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					recu <- data.String()
					say("250 ok")
					continue
				}
				data.WriteString(line)
				continue
			}
			switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
			case strings.HasPrefix(cmd, "EHLO"):
				if starttls {
					say("250-factice")
					say("250 STARTTLS")
				} else {
					say("250 factice")
				}
			case cmd == "DATA":
				inData = true
				say("354 allez-y")
			case cmd == "QUIT":
				say("221 au revoir")
				return
			default:
				say("250 ok")
			}
		}
	}()
	return ln.Addr().String(), recu
}

func TestMail(t *testing.T) {
	addr, recu := smtpFactice(t, false)
	m := &mailer{server: addr, security: "aucun", from: "mire@exemple.be", to: []string{"moi@exemple.be", "toi@exemple.be"}}
	if err := m.Send(context.Background(), message()); err != nil {
		t.Fatal(err)
	}
	brut := <-recu
	for _, attendu := range []string{"Subject: Mire : alerte critique", "To: moi@exemple.be, toi@exemple.be", "charset=utf-8",
		"Coupure d'Internet.", "https://mire.exemple.be/evenements"} {
		if !strings.Contains(brut, attendu) {
			t.Errorf("message sans %q :\n%s", attendu, brut)
		}
	}
}

func TestMailSansSTARTTLSEstRefuse(t *testing.T) {
	// Un serveur qui ne propose pas STARTTLS ne doit pas recevoir le mot de
	// passe en clair : l'envoi s'arrete avant l'authentification.
	addr, _ := smtpFactice(t, false)
	m := &mailer{server: addr, security: "starttls", user: "moi", password: "secret",
		from: "mire@exemple.be", to: []string{"moi@exemple.be"}}
	err := m.Send(context.Background(), message())
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Errorf("erreur %v, attendu un refus faute de STARTTLS", err)
	}
}

func TestUnSujetAccentueEstEncode(t *testing.T) {
	m := &mailer{from: "mire@exemple.be", to: []string{"moi@exemple.be"}}
	brut := string(m.compose(Message{Title: "Mire : à surveiller", Body: "Ligne à surveiller."}))
	if !strings.Contains(brut, "Subject: =?utf-8?q?Mire_:_=C3=A0_surveiller?=") {
		t.Errorf("sujet mal encode :\n%s", brut)
	}
	if !strings.Contains(brut, "Ligne =C3=A0 surveiller.") {
		t.Errorf("corps mal encode :\n%s", brut)
	}
}
