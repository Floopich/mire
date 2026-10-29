package mqtt_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/mqtt"
	"github.com/floopich/mire/internal/mqtt/mqtttest"
)

func attendre(t *testing.T, ok func() bool) {
	t.Helper()
	for range 100 {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("le serveur n'a pas reçu ce qui était attendu")
}

func TestConnexionPublicationEtAdieu(t *testing.T) {
	b := mqtttest.Start(t)
	c, err := mqtt.Dial(context.Background(), mqtt.Options{
		Server: b.URL(), User: "mire", Password: "secret", ClientID: "mire-test",
		Will: &mqtt.Message{Topic: "mire/disponibilite", Payload: []byte("offline"), Retain: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Un message plus long que 127 octets encode sa longueur sur deux octets.
	long := strings.Repeat("é", 200)
	if err := c.Publish(mqtt.Message{Topic: "mire/etat", Payload: []byte(long), Retain: true}); err != nil {
		t.Fatal(err)
	}
	if err := c.Publish(mqtt.Message{Topic: "mire/passage", Payload: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if err := c.Ping(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	attendre(t, func() bool { return b.Closes() == 1 })

	cs := b.Connects()
	if len(cs) != 1 || cs[0].ClientID != "mire-test" || cs[0].User != "mire" || cs[0].Password != "secret" || cs[0].KeepAlive != 60 {
		t.Fatalf("connexion reçue : %+v", cs)
	}
	if w := cs[0].Will; w == nil || w.Topic != "mire/disponibilite" || string(w.Payload) != "offline" || !w.Retain {
		t.Errorf("dernière volonté : %+v", w)
	}
	ms := b.Messages()
	if len(ms) != 2 || ms[0].Topic != "mire/etat" || string(ms[0].Payload) != long || !ms[0].Retain || ms[1].Retain {
		t.Errorf("publications reçues : %+v", ms)
	}
	if b.Pings() != 1 {
		t.Errorf("%d pings reçus", b.Pings())
	}
}

func TestRefusDuServeur(t *testing.T) {
	b := mqtttest.Start(t)
	b.Refuse = 4
	_, err := mqtt.Dial(context.Background(), mqtt.Options{Server: b.URL(), User: "mire", Password: "faux"})
	if !errors.Is(err, mqtt.ErrRefused) || !strings.Contains(err.Error(), "identifiant ou mot de passe incorrect") {
		t.Fatalf("erreur %v", err)
	}
}

func TestServeurInjoignableOuAdresseInvalide(t *testing.T) {
	ctx := context.Background()
	if _, err := mqtt.Dial(ctx, mqtt.Options{Server: "mqtt://127.0.0.1:1", Timeout: time.Second}); err == nil ||
		!strings.Contains(err.Error(), "connexion refusée, aucun serveur MQTT sur ce port") {
		t.Errorf("port fermé : %v", err)
	}
	if _, err := mqtt.Dial(ctx, mqtt.Options{Server: "http://exemple.be"}); err == nil {
		t.Error("schéma http accepté")
	}
}

func TestAbonnement(t *testing.T) {
	b := mqtttest.Start(t)
	c, err := mqtt.Dial(context.Background(), mqtt.Options{Server: b.URL(), ClientID: "mire-test"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if err := c.Subscribe("mire/commande"); err != nil {
		t.Fatal(err)
	}
	attendre(t, func() bool { return len(b.Subscriptions()) == 1 })
	if s := b.Subscriptions(); s[0] != "mire/commande" {
		t.Fatalf("abonnements : %v", s)
	}
	b.Send(mqtt.Message{Topic: "mire/commande", Payload: []byte("PRESS"), Retain: true})
	select {
	case m := <-c.Messages():
		if m.Topic != "mire/commande" || string(m.Payload) != "PRESS" || !m.Retain {
			t.Errorf("message reçu : %+v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("aucun message reçu")
	}
	// La lecture continue ne gene pas le ping.
	if err := c.Ping(); err != nil {
		t.Fatal(err)
	}
}

func TestConnexionPerdue(t *testing.T) {
	b := mqtttest.Start(t)
	c, err := mqtt.Dial(context.Background(), mqtt.Options{Server: b.URL(), Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	select {
	case <-c.Done():
		if c.Err() == nil {
			t.Error("la cause de la fin de connexion manque")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("la fin de connexion n'est pas signalée")
	}
	if err := c.Ping(); err == nil {
		t.Error("un ping sur une connexion fermée doit échouer")
	}
}
