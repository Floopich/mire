package metrics

import (
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/operator"
	"github.com/floopich/mire/internal/status"
	"github.com/floopich/mire/internal/store"
)

func ligne(t *testing.T) status.Status {
	t.Helper()
	snap := docsis.RawSnapshot{DOCSIS: "3.1",
		Downstream: []docsis.RawDownstream{
			{ChannelID: 159, Modulation: "OFDM", Frequency: "706.5 MHz", PowerLevel: "6.2", MSE: "-43.5", CorrErrors: 3922065920, NonCorrErrors: 17195},
			{ChannelID: 1, Modulation: "256QAM", Frequency: "442 MHz", PowerLevel: "2.1", MSE: "-40.4"},
		},
		Upstream: []docsis.RawUpstream{{ChannelID: 1, Modulation: "32QAM", Frequency: "37.2 MHz", PowerLevel: "35.0"}},
	}.Normalize(time.Date(2026, 10, 26, 21, 0, 0, 0, time.UTC))
	p, err := operator.Load("voo")
	if err != nil {
		t.Fatal(err)
	}
	avg := 12.5
	now := time.Date(2026, 10, 26, 21, 5, 0, 0, time.UTC)
	return status.Status{HasData: true, Snapshot: snap, Health: analyze.Snapshot(snap, p),
		HasProbe: true, Probe: store.ProbeSample{Rounds: 20, Failed: 1, RTTAvg: &avg},
		Now: now, Fresh: true, Outages24h: 1, Outages30d: 2,
		HasLastOutage: true, LastOutage: store.Outage{Start: now.Add(-2 * time.Hour), End: now.Add(-time.Hour)},
		LastRestart: now.Add(-3 * time.Hour)}
}

func ecrire(t *testing.T, s status.Status, info Info) string {
	t.Helper()
	var b bytes.Buffer
	if err := Write(&b, s, info); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestMetriquesDeLaLigne(t *testing.T) {
	out := ecrire(t, ligne(t), Info{Version: "v0.2.0", Modem: `Technicolor "CGA4233"`, Profile: "voo"})
	for _, voulu := range []string{
		`mire_info{version="v0.2.0",modem="Technicolor \"CGA4233\"",profile="voo"} 1`,
		"mire_last_reading_timestamp_seconds 1793048400",
		"mire_line_level 1",
		`mire_channels{direction="downstream"} 2`,
		`mire_downstream_power_dbmv{channel="1",modulation="256QAM"} 2.1`,
		`mire_downstream_snr_db{channel="159",modulation="OFDM"} 43.5`,
		`mire_downstream_frequency_mhz{channel="159",modulation="OFDM"} 706.5`,
		`mire_downstream_corrected_total{channel="159",modulation="OFDM"} 3922065920`,
		`mire_upstream_power_dbmv{channel="1",modulation="32QAM"} 35`,
		"mire_internet_up 1",
		"mire_probe_latency_seconds 0.0125",
		"mire_probe_loss_ratio 0.05",
		"mire_modem_up 1",
		`mire_outages{window="24h"} 1`,
		`mire_outages{window="30d"} 2`,
		"mire_last_outage_duration_seconds 3600",
		"mire_modem_last_restart_timestamp_seconds 1793037900",
	} {
		if !strings.Contains(out, voulu+"\n") {
			t.Errorf("absent : %s", voulu)
		}
	}
	// Les canaux sont ranges par numero.
	if strings.Index(out, `power_dbmv{channel="1"`) > strings.Index(out, `power_dbmv{channel="159"`) {
		t.Error("canaux non ranges par numero")
	}
}

// Chaque ligne respecte le format texte : une famille est declaree une fois,
// avant ses echantillons.
func TestFormatTexteValide(t *testing.T) {
	out := ecrire(t, ligne(t), Info{Version: "dev"})
	sample := regexp.MustCompile(`^([a-z_]+)(\{[^}]*\})? (-?[0-9.e+]+|NaN|[+-]Inf)$`)
	declared := map[string]bool{}
	for _, l := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		switch {
		case strings.HasPrefix(l, "# HELP "):
		case strings.HasPrefix(l, "# TYPE "):
			name := strings.Fields(l)[2]
			if declared[name] {
				t.Errorf("famille declaree deux fois : %s", name)
			}
			declared[name] = true
		default:
			m := sample.FindStringSubmatch(l)
			if m == nil {
				t.Errorf("ligne invalide : %q", l)
			} else if !declared[m[1]] {
				t.Errorf("echantillon sans TYPE : %q", l)
			}
		}
	}
}

func TestSansReleveNiSonde(t *testing.T) {
	out := ecrire(t, status.Status{}, Info{Version: "dev"})
	if !strings.Contains(out, "mire_info") || strings.Contains(out, "mire_line_level") || strings.Contains(out, "mire_internet_up") {
		t.Errorf("sortie inattendue :\n%s", out)
	}
	coupure := ecrire(t, status.Status{Outage: true}, Info{})
	if !strings.Contains(coupure, "mire_internet_up 0\n") {
		t.Error("une coupure en cours doit donner mire_internet_up 0")
	}
}

// Le tableau de bord Grafana du depot ne lit que des metriques publiees : un
// renommage ici casserait ses graphiques sans bruit.
func TestTableauDeBordGrafana(t *testing.T) {
	raw, err := os.ReadFile("../../docs/grafana/mire.json")
	if err != nil {
		t.Fatal(err)
	}
	var dash struct {
		Panels []struct {
			Title   string `json:"title"`
			Targets []struct {
				Expr string `json:"expr"`
			} `json:"targets"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(raw, &dash); err != nil {
		t.Fatalf("JSON invalide : %v", err)
	}
	out := ecrire(t, ligne(t), Info{Version: "dev"})
	name := regexp.MustCompile(`mire_[a-z_]+`)
	n := 0
	for _, p := range dash.Panels {
		for _, tg := range p.Targets {
			for _, m := range name.FindAllString(tg.Expr, -1) {
				n++
				if !strings.Contains(out, "# TYPE "+m+" ") {
					t.Errorf("panneau %q : %s n'est pas publiée", p.Title, m)
				}
			}
		}
	}
	if n == 0 {
		t.Fatal("aucune métrique lue par le tableau de bord")
	}
}
