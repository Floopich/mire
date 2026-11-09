package webview

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/events"
	"github.com/floopich/mire/internal/operator"
	"github.com/floopich/mire/internal/service"
	"github.com/floopich/mire/internal/settings"
	"github.com/floopich/mire/internal/store"
)

// Verdict explique : pour chaque mesure hors plage, le seuil franchi et
// depuis quand. La date se lit dans l'historique du canal, rejuge avec les
// seuils en vigueur ; elle n'est jamais plus precise que les releves.

const (
	// onsetLookback borne la recherche du debut d'un constat.
	onsetLookback = 30 * 24 * time.Hour
	// maxReasons borne les raisons listees sous le verdict.
	maxReasons = 5
)

// reason est une mesure hors plage, expliquee.
// channelWhere nomme un canal avec sa frequence, que le technicien lit
// avant le numero : « Canal descendant 12 (538 MHz) ».
func channelWhere(dir string, ch analyze.ChannelHealth) string {
	s := "Canal " + dir + " " + strconv.Itoa(ch.ChannelID)
	if ch.FrequencyMHz != nil {
		s += " (" + docsis.MHz(*ch.FrequencyMHz) + ")"
	}
	return s
}

type reason struct {
	Level   analyze.Level
	Where   string // « Canal descendant 12 (538 MHz) »
	Href    string
	Metric  string
	Message string
	// Headline est la mesure et sa valeur : « SNR : 33,1 dB, sous le seuil
	// nominal ».
	Headline string
	Limit    string
	// Since dit depuis quand, vide quand la mesure ne se date pas (erreurs,
	// jugees sur une fenetre glissante).
	Since string
	start time.Time
}

// onsetScan date un constat en remontant les releves d'un canal, du plus
// recent au plus ancien. Le constat commence au plus ancien releve d'une
// suite ininterrompue hors plage. Un trou dans les releves, ou la fin de
// l'historique relu, ne laisse qu'une borne : « depuis au moins ».
type onsetScan struct {
	maxGap  time.Duration
	start   time.Time
	atLeast bool
	done    bool
}

// step prend un releve ; il renvoie false quand le debut est trouve.
func (o *onsetScan) step(t time.Time, lv analyze.Level, judged bool) bool {
	switch {
	case !o.start.IsZero() && o.start.Sub(t) > o.maxGap:
		o.atLeast, o.done = true, true
	case !judged || lv < analyze.Warning:
		o.done = true
	default:
		o.start = t
	}
	return !o.done
}

// finish clot la recherche : un historique epuise ne donne qu'une borne.
func (o *onsetScan) finish() {
	if !o.done && !o.start.IsZero() {
		o.atLeast = true
	}
}

// sinceText rend le debut d'un constat : « depuis 2 h 10, le 03/11/2026 à
// 14:05 ».
func sinceText(start, latest, now time.Time, atLeast bool) string {
	switch {
	case start.IsZero():
		return ""
	case start.Equal(latest) && !atLeast:
		return "au dernier relevé seulement"
	case atLeast:
		return "depuis au moins " + events.Duration(now.Sub(start)) + ", le " + FmtTime(start)
	}
	return "depuis " + events.Duration(now.Sub(start)) + ", le " + FmtTime(start)
}

// explain liste les mesures hors plage du releve, les plus graves et les
// plus anciennes d'abord.
func (s *Server) explain(ctx context.Context, st service.State, snap docsis.Snapshot, h analyze.Health, hours int) ([]reason, error) {
	if !h.Qualified {
		return nil, nil
	}
	t := analyze.Effective(st.Profile.Thresholds, analyze.Options{OFDMAFloor: st.Values.Int(settings.OFDMAFloor)})
	gap := max(3*st.Interval, 15*time.Minute)
	now := s.opts.Now()
	var out []reason
	add := func(dir, path string, ch analyze.ChannelHealth, date func(metric string) (onsetScan, error)) error {
		for _, f := range ch.Findings {
			if f.Level < analyze.Warning {
				continue
			}
			r := reason{Level: f.Level, Metric: f.Metric, Message: f.Message, Limit: f.Limit, Headline: ucFirst(f.Metric) + " : " + f.Message,
				Where: channelWhere(dir, ch),
				Href:  path + strconv.Itoa(ch.ChannelID) + "?heures=" + strconv.Itoa(hours)}
			if f.Metric != "erreurs" {
				o, err := date(f.Metric)
				if err != nil {
					return err
				}
				r.start = o.start
				r.Since = sinceText(o.start, snap.Timestamp, now, o.atLeast)
			}
			out = append(out, r)
		}
		return nil
	}
	from := snap.Timestamp.Add(-onsetLookback)
	for _, ch := range h.Downstream {
		if ch.Level < analyze.Warning {
			continue
		}
		err := add("descendant", "/descendant/", ch, func(metric string) (onsetScan, error) {
			o := onsetScan{maxGap: gap}
			err := store.DownstreamBack(ctx, s.db, ch.ChannelID, from, snap.Timestamp, func(ts time.Time, c docsis.Downstream) bool {
				lv, ok := analyze.DownstreamMetric(c, t, metric)
				return o.step(ts, lv, ok)
			})
			o.finish()
			return o, err
		})
		if err != nil {
			return nil, err
		}
	}
	for _, ch := range h.Upstream {
		if ch.Level < analyze.Warning {
			continue
		}
		err := add("montant", "/montant/", ch, func(metric string) (onsetScan, error) {
			o := onsetScan{maxGap: gap}
			err := store.UpstreamBack(ctx, s.db, ch.ChannelID, from, snap.Timestamp, func(ts time.Time, c docsis.Upstream) bool {
				lv, ok := analyze.UpstreamMetric(c, t, metric)
				return o.step(ts, lv, ok)
			})
			o.finish()
			return o, err
		})
		if err != nil {
			return nil, err
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Level != out[j].Level {
			return out[i].Level > out[j].Level
		}
		a, b := out[i].start, out[j].start
		return !a.IsZero() && (b.IsZero() || a.Before(b))
	})
	return out, nil
}

// basisText dit d'ou viennent les seuils du profil.
func basisText(p operator.Profile) string {
	if !p.Calibrated || p.Basis == "" {
		return ""
	}
	return "Seuils " + p.Name + " : " + p.Basis + "."
}

// ucFirst met une majuscule au premier mot : « Puissance descendante ».
func ucFirst(s string) string {
	for i, r := range s {
		return strings.ToUpper(string(r)) + s[i+len(string(r)):]
	}
	return s
}

// channelState juge un canal au dernier releve : toutes ses mesures, les
// mesures hors plage datees.
func (s *Server) channelState(ctx context.Context, direction string, id, hours int) ([]reason, error) {
	snap, err := store.LatestSnapshot(ctx, s.db)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	st := s.opts.Service.State()
	h, err := st.Analyze(ctx, s.db, snap)
	if err != nil || !h.Qualified {
		return nil, err
	}
	list := h.Downstream
	if direction == "montant" {
		list = h.Upstream
	}
	var ch *analyze.ChannelHealth
	for i := range list {
		if list[i].ChannelID == id {
			ch = &list[i]
		}
	}
	if ch == nil {
		return nil, nil
	}
	dated, err := s.explain(ctx, st, snap, h, hours)
	if err != nil {
		return nil, err
	}
	where := channelWhere(direction, *ch)
	since := map[string]string{}
	for _, r := range dated {
		if r.Where == where {
			since[r.Metric] = r.Since
		}
	}
	out := make([]reason, 0, len(ch.Findings))
	for _, f := range ch.Findings {
		out = append(out, reason{Level: f.Level, Metric: f.Metric, Message: f.Message, Limit: f.Limit,
			Headline: ucFirst(f.Metric) + " : " + f.Message, Since: since[f.Metric]})
	}
	return out, nil
}
