package homeassistant

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/mqtt"
	"github.com/floopich/mire/internal/mqtt/mqtttest"
	"github.com/floopich/mire/internal/status"
	"github.com/floopich/mire/internal/store"
)

func ligne() status.Status {
	p1, p2, snr, us := 2.1, 6.2, 40.4, 35.5
	avg := 12.34
	now := time.Date(2026, 10, 26, 21, 30, 0, 0, time.UTC)
	return status.Status{
		Now: now, Fresh: true, Outages24h: 1, Outages30d: 3, Uncorrected24h: 42,
		HasLastOutage: true, LastOutage: store.Outage{Start: now.Add(-3 * time.Hour), End: now.Add(-2*time.Hour - 15*time.Minute)},
		LastRestart: now.Add(-24 * time.Hour),
		HasData:     true,
		Snapshot: docsis.Snapshot{Timestamp: time.Date(2026, 10, 26, 21, 0, 0, 0, time.UTC),
			Downstream: []docsis.Downstream{{ChannelID: 1, PowerDBmV: &p1, SNRDB: &snr}, {ChannelID: 159, PowerDBmV: &p2}},
			Upstream:   []docsis.Upstream{{ChannelID: 1, PowerDBmV: &us}}},
		Health: analyze.Health{Qualified: true, Level: analyze.Warning,
			Downstream: []analyze.ChannelHealth{{ChannelID: 159, Level: analyze.Warning,
				Findings: []analyze.Finding{{Level: analyze.Warning, Metric: "puissance descendante", Message: "9,1\u00a0dBmV, hors de la plage nominale"}}}}},
		HasProbe: true, Probe: store.ProbeSample{Rounds: 10, Failed: 0, RTTAvg: &avg},
	}
}

func TestEtatPublie(t *testing.T) {
	var got map[string]any
	if err := json.Unmarshal(State(ligne()), &got); err != nil {
		t.Fatal(err)
	}
	voulu := map[string]any{
		"ligne": "À surveiller", "internet": "ON", "canaux_attention": 1.0,
		"puissance_descendante_min": 2.1, "puissance_descendante_max": 6.2, "snr_min": 40.4, "emission_max": 35.5,
		"canaux_descendants": 2.0, "canaux_montants": 1.0, "latence": 12.3, "pertes": 0.0,
		"dernier_releve": "2026-10-26T21:00:00Z",
		// Sondes.
		"signal": "ON", "modem": "ON",
		"diagnostic":       "Ligne à surveiller : canal descendant 159, puissance descendante 9,1\u00a0dBmV, hors de la plage nominale.",
		"derniere_coupure": "2026-10-26T18:30:00Z", "duree_derniere_coupure": 45.0,
		"coupures_24h": 1.0, "coupures_30j": 3.0, "dernier_redemarrage": "2026-10-25T21:30:00Z",
		"erreurs_non_corrigees_24h": 42.0,
	}
	for k, v := range voulu {
		if got[k] != v {
			t.Errorf("%s = %v, attendu %v", k, got[k], v)
		}
	}
	// Sans releve ni sonde : ligne non qualifiee, le reste inconnu (null).
	if err := json.Unmarshal(State(status.Status{}), &got); err != nil {
		t.Fatal(err)
	}
	if got["ligne"] != "Non qualifié" || got["internet"] != nil || got["latence"] != nil ||
		got["signal"] != nil || got["modem"] != "OFF" || got["derniere_coupure"] != nil {
		t.Errorf("état vide : %v", got)
	}
	// Une ligne seulement toleree n'est pas un probleme de signal.
	tol := ligne()
	tol.Health.Level = analyze.Tolerated
	if err := json.Unmarshal(State(tol), &got); err != nil || got["signal"] != "OFF" {
		t.Errorf("toléré : signal %v", got["signal"])
	}
	if c := clip(strings.Repeat("é", 300), maxState); len([]rune(c)) != maxState || !strings.HasSuffix(c, "…") {
		t.Errorf("diagnostic tronqué : %d caractères", len([]rune(c)))
	}
}

func TestDecouverte(t *testing.T) {
	msgs := Discovery(Config{Prefix: "homeassistant", Version: "v0.2.0", Modem: "Technicolor CGA4233",
		Link: "https://mire.exemple.be"})
	if len(msgs) != len(entities)+1 {
		t.Fatalf("%d messages pour %d capteurs", len(msgs), len(entities))
	}
	// Sans redemarrage possible, le bouton est retire : configuration vide.
	if m := msgs[0]; m.Topic != "homeassistant/button/mire/redemarrer/config" || len(m.Payload) != 0 || !m.Retain {
		t.Errorf("bouton : %s %q", m.Topic, m.Payload)
	}
	topics := map[string]bool{}
	for _, m := range msgs[1:] {
		if !m.Retain || !strings.HasPrefix(m.Topic, "homeassistant/") || !strings.HasSuffix(m.Topic, "/config") {
			t.Errorf("message de découverte : %s retenu=%v", m.Topic, m.Retain)
		}
		topics[m.Topic] = true
		var c map[string]any
		if err := json.Unmarshal(m.Payload, &c); err != nil {
			t.Fatal(err)
		}
		if id, _ := c["default_entity_id"].(string); !strings.HasPrefix(m.Topic, "homeassistant/"+strings.Split(id, ".")[0]+"/mire/") ||
			!strings.HasPrefix(strings.SplitN(id, ".", 2)[1], "mire_") || c["object_id"] != nil {
			t.Errorf("%s : identifiant %v", m.Topic, c["default_entity_id"])
		}
		dev := c["device"].(map[string]any)
		if c["state_topic"] != stateTopic || c["availability_topic"] != availability ||
			dev["configuration_url"] != "https://mire.exemple.be" || dev["model"] != "Technicolor CGA4233" {
			t.Errorf("%s : %v", m.Topic, c)
		}
		// Chaque gabarit lit une cle que l'etat publie porte.
		tpl := c["value_template"].(string)
		key := strings.TrimSuffix(strings.TrimPrefix(tpl, "{{ value_json."), " }}")
		if !strings.Contains(string(State(ligne())), `"`+key+`":`) {
			t.Errorf("%s lit %q, absent de l'état", m.Topic, key)
		}
	}
	for _, voulu := range []string{"homeassistant/binary_sensor/mire/internet/config", "homeassistant/sensor/mire/ligne/config",
		"homeassistant/binary_sensor/mire/signal/config", "homeassistant/sensor/mire/diagnostic/config"} {
		if !topics[voulu] {
			t.Errorf("absent : %s", voulu)
		}
	}
}

func TestPublicationVersLeServeur(t *testing.T) {
	b := mqtttest.Start(t)
	ctx, cancel := context.WithCancel(context.Background())
	p := New(Config{Server: b.URL(), User: "mire", Password: "secret"},
		func(context.Context) (status.Status, error) { return ligne(), nil },
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && b.Retained()[stateTopic] == "" {
		time.Sleep(20 * time.Millisecond)
	}
	r := b.Retained()
	if r[stateTopic] != string(State(ligne())) || r[availability] != "online" {
		t.Fatalf("retenus : état %q, disponibilité %q", r[stateTopic], r[availability])
	}
	if r["homeassistant/sensor/mire/ligne/config"] == "" {
		t.Error("découverte non publiée (préfixe par défaut)")
	}
	if c := b.Connects(); len(c) != 1 || c[0].Will == nil || string(c[0].Will.Payload) != "offline" {
		t.Errorf("connexion : %+v", c)
	}
	if p.Err() != nil || !p.Connected() {
		t.Errorf("après connexion : connecté %v, erreur %v", p.Connected(), p.Err())
	}

	// A l'arret, les capteurs passent indisponibles, puis Mire se deconnecte.
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("arrêt : %v", err)
	}
	for range 100 {
		if b.Closes() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if b.Retained()[availability] != "offline" || b.Closes() != 1 {
		t.Errorf("adieu : disponibilité %q, déconnexions %d", b.Retained()[availability], b.Closes())
	}
}

func TestEssaiRefuse(t *testing.T) {
	b := mqtttest.Start(t)
	b.Refuse = 5
	err := Test(context.Background(), Config{Server: b.URL()})
	if err == nil || !strings.Contains(err.Error(), "accès non autorisé") {
		t.Fatalf("essai : %v", err)
	}
	if err := Test(context.Background(), Config{}); err == nil {
		t.Error("essai sans serveur accepté")
	}
}

func TestBoutonRedemarrer(t *testing.T) {
	b := mqtttest.Start(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	appuis := make(chan struct{}, 4)
	p := New(Config{Server: b.URL(), Restart: func(context.Context) { appuis <- struct{}{} }},
		func(context.Context) (status.Status, error) { return ligne(), nil },
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	go func() { _ = p.Run(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(b.Subscriptions()) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if s := b.Subscriptions(); len(s) != 1 || s[0] != restartTopic {
		t.Fatalf("abonnements : %v", s)
	}
	var c map[string]any
	if err := json.Unmarshal([]byte(b.Retained()["homeassistant/button/mire/redemarrer/config"]), &c); err != nil {
		t.Fatal(err)
	}
	if c["command_topic"] != restartTopic || c["device_class"] != "restart" || c["state_topic"] != nil {
		t.Errorf("bouton : %v", c)
	}

	// Un message retenu ou inattendu n'est pas un appui.
	b.Send(mqtt.Message{Topic: restartTopic, Payload: []byte(press), Retain: true})
	b.Send(mqtt.Message{Topic: restartTopic, Payload: []byte("autre")})
	b.Send(mqtt.Message{Topic: restartTopic, Payload: []byte(press)})
	select {
	case <-appuis:
	case <-time.After(3 * time.Second):
		t.Fatal("l'appui n'a pas redémarré le modem")
	}
	select {
	case <-appuis:
		t.Error("un seul appui attendu")
	case <-time.After(200 * time.Millisecond):
	}
}
