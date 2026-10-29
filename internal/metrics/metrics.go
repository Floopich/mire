// Package metrics ecrit l'etat de la ligne au format texte de Prometheus,
// avec les conventions de nommage Prometheus et des descriptions en
// francais.
package metrics

import (
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/status"
)

// ContentType est le type du format texte de Prometheus.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

// Info decrit l'instance.
type Info struct {
	Version string
	Modem   string
	Profile string
}

const levels = "0 non qualifié, 1 bon, 2 toléré, 3 à surveiller, 4 critique"

// Write ecrit les metriques de l'etat present.
func Write(w io.Writer, s status.Status, info Info) error {
	m := &writer{w: w}
	m.family("mire_info", "gauge", "Instance de Mire : version, modem et profil d'opérateur.")
	m.sample("mire_info", labels{"version", info.Version, "modem", info.Modem, "profile", info.Profile}, 1)

	if s.HasData {
		sum := s.Summarize()
		m.one("mire_last_reading_timestamp_seconds", "gauge", "Heure du dernier relevé du modem, en secondes Unix.",
			float64(s.Snapshot.Timestamp.Unix()))
		qualified := 0.0
		if s.Health.Qualified {
			qualified = 1
		}
		m.one("mire_line_qualified", "gauge", "1 si le profil d'opérateur permet de juger la ligne.", qualified)
		m.one("mire_line_level", "gauge", "État de la ligne : "+levels+".", float64(s.Health.Level))
		m.one("mire_channels_attention", "gauge", "Canaux à surveiller ou critiques.", float64(sum.Attention))
		m.family("mire_channels", "gauge", "Canaux du dernier relevé, par voie.")
		m.sample("mire_channels", labels{"direction", "downstream"}, float64(sum.DSCount))
		m.sample("mire_channels", labels{"direction", "upstream"}, float64(sum.USCount))

		ds := levelsByChannel(s.Health.Downstream)
		us := levelsByChannel(s.Health.Upstream)
		down := slices.Clone(s.Snapshot.Downstream)
		sort.Slice(down, func(i, j int) bool { return down[i].ChannelID < down[j].ChannelID })
		up := slices.Clone(s.Snapshot.Upstream)
		sort.Slice(up, func(i, j int) bool { return up[i].ChannelID < up[j].ChannelID })

		m.family("mire_downstream_power_dbmv", "gauge", "Puissance reçue par canal descendant, en dBmV.")
		for _, c := range down {
			m.optional("mire_downstream_power_dbmv", channel(c.ChannelID, c.Modulation), c.PowerDBmV)
		}
		m.family("mire_downstream_snr_db", "gauge", "Rapport signal/bruit par canal descendant, en dB.")
		for _, c := range down {
			m.optional("mire_downstream_snr_db", channel(c.ChannelID, c.Modulation), c.SNRDB)
		}
		m.family("mire_downstream_frequency_mhz", "gauge", "Fréquence par canal descendant, en MHz.")
		for _, c := range down {
			m.optional("mire_downstream_frequency_mhz", channel(c.ChannelID, c.Modulation), c.FrequencyMHz)
		}
		m.family("mire_downstream_corrected_total", "counter",
			"Erreurs corrigées par canal descendant, depuis le démarrage du modem.")
		for _, c := range down {
			m.sample("mire_downstream_corrected_total", channel(c.ChannelID, c.Modulation), float64(c.CorrErrors))
		}
		m.family("mire_downstream_uncorrected_total", "counter",
			"Erreurs non corrigées par canal descendant, depuis le démarrage du modem.")
		for _, c := range down {
			m.sample("mire_downstream_uncorrected_total", channel(c.ChannelID, c.Modulation), float64(c.NonCorrErrors))
		}
		m.family("mire_downstream_level", "gauge", "État par canal descendant : "+levels+".")
		for _, c := range down {
			m.sample("mire_downstream_level", channel(c.ChannelID, c.Modulation), float64(ds[c.ChannelID]))
		}

		m.family("mire_upstream_power_dbmv", "gauge", "Puissance émise par canal montant, en dBmV.")
		for _, c := range up {
			m.optional("mire_upstream_power_dbmv", channel(c.ChannelID, c.Modulation), c.PowerDBmV)
		}
		m.family("mire_upstream_frequency_mhz", "gauge", "Fréquence par canal montant, en MHz.")
		for _, c := range up {
			m.optional("mire_upstream_frequency_mhz", channel(c.ChannelID, c.Modulation), c.FrequencyMHz)
		}
		m.family("mire_upstream_level", "gauge", "État par canal montant : "+levels+".")
		for _, c := range up {
			m.sample("mire_upstream_level", channel(c.ChannelID, c.Modulation), float64(us[c.ChannelID]))
		}
	}

	if s.HasData {
		fresh := 0.0
		if s.Fresh {
			fresh = 1
		}
		m.one("mire_modem_up", "gauge", "1 si le modem répond et que le dernier relevé est récent.", fresh)
	}
	m.family("mire_outages", "gauge", "Coupures d'Internet constatées par la sonde, hors réseau local, sur la fenêtre.")
	m.sample("mire_outages", labels{"window", "24h"}, float64(s.Outages24h))
	m.sample("mire_outages", labels{"window", "30d"}, float64(s.Outages30d))
	if s.HasLastOutage {
		m.one("mire_last_outage_start_timestamp_seconds", "gauge", "Début de la dernière coupure d'Internet, en secondes Unix.",
			float64(s.LastOutage.Start.Unix()))
		m.one("mire_last_outage_duration_seconds", "gauge", "Durée de la dernière coupure d'Internet, en cours ou finie, en secondes.",
			math.Round(s.LastOutage.Duration(s.Now).Seconds()))
	}
	if !s.LastRestart.IsZero() {
		m.one("mire_modem_last_restart_timestamp_seconds", "gauge", "Dernier redémarrage du modem constaté, en secondes Unix.",
			float64(s.LastRestart.Unix()))
	}
	if s.HasProbe || s.Outage {
		up := 1.0
		if s.Outage {
			up = 0
		}
		m.one("mire_internet_up", "gauge", "1 si Internet répond, 0 pendant une coupure constatée par la sonde.", up)
	}
	if s.HasProbe {
		if s.Probe.RTTAvg != nil {
			m.one("mire_probe_latency_seconds", "gauge", "Latence moyenne de la dernière tranche de la sonde, en secondes.",
				*s.Probe.RTTAvg/1000)
		}
		if loss, ok := s.Loss(); ok {
			m.one("mire_probe_loss_ratio", "gauge", "Part des tentatives perdues de la dernière tranche de la sonde, de 0 à 1.",
				loss/100)
		}
	}
	return m.err
}

type labels []string // paires nom, valeur

func channel(id int, modulation string) labels {
	return labels{"channel", strconv.Itoa(id), "modulation", modulation}
}

func levelsByChannel(list []analyze.ChannelHealth) map[int]analyze.Level {
	out := make(map[int]analyze.Level, len(list))
	for _, c := range list {
		out[c.ChannelID] = c.Level
	}
	return out
}

type writer struct {
	w   io.Writer
	err error
}

func (m *writer) printf(format string, a ...any) {
	if m.err == nil {
		_, m.err = fmt.Fprintf(m.w, format, a...)
	}
}

func (m *writer) family(name, kind, help string) {
	m.printf("# HELP %s %s\n# TYPE %s %s\n", name, escapeHelp(help), name, kind)
}

func (m *writer) one(name, kind, help string, v float64) {
	m.family(name, kind, help)
	m.sample(name, nil, v)
}

func (m *writer) optional(name string, l labels, v *float64) {
	if v != nil {
		m.sample(name, l, *v)
	}
}

func (m *writer) sample(name string, l labels, v float64) {
	var b strings.Builder
	b.WriteString(name)
	if len(l) > 0 {
		b.WriteByte('{')
		for i := 0; i+1 < len(l); i += 2 {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(l[i])
			b.WriteString(`="`)
			b.WriteString(escapeLabel(l[i+1]))
			b.WriteByte('"')
		}
		b.WriteByte('}')
	}
	m.printf("%s %s\n", b.String(), number(v))
}

func number(v float64) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	}
	// Un entier s'ecrit en entier : horodatages et compteurs restent lisibles.
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

var (
	labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	helpEscaper  = strings.NewReplacer(`\`, `\\`, "\n", `\n`)
)

func escapeLabel(s string) string { return labelEscaper.Replace(s) }
func escapeHelp(s string) string  { return helpEscaper.Replace(s) }
