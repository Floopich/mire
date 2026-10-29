// Package demo fait tourner Mire sur une ligne fictive : MIRE_DEMO=true.
//
// Un modem imaginaire, deterministe, rend des releves plausibles d'un
// raccordement au reseau VOO ; quatre-vingt-dix jours d'historique sont
// ecrits au demarrage par les memes chemins que la collecte, avec quelques
// incidents mis en scene : une degradation reparee par un technicien, des
// coupures, un redemarrage, un pic d'erreurs. Rien ne touche un vrai modem,
// et la base vit dans un dossier temporaire.
package demo

import (
	"context"
	"database/sql"
	"fmt"
	"hash/fnv"
	"math"
	"sync"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/auth"
	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/events"
	"github.com/floopich/mire/internal/modem"
	"github.com/floopich/mire/internal/probe"
	"github.com/floopich/mire/internal/store"
)

// ModelID est le modele du modem fictif ; il ne figure dans aucune liste.
const ModelID = "demo"

// Days est la profondeur de l'historique.
const Days = 90

// Password est celui de l'interface ; le mode demo ne le demande pas.
const Password = "demonstration"

// Line est la ligne fictive. Son origine est l'instant du lancement : les
// incidents sont places par rapport a elle.
type Line struct {
	Origin time.Time
	Now    func() time.Time

	mu       sync.Mutex
	restarts []time.Time // redemarrages demandes pendant la demo
}

// New prepare une ligne dont l'historique s'arrete a origin.
func New(origin time.Time) *Line {
	return &Line{Origin: origin.UTC(), Now: time.Now}
}

// Model est le modele a inscrire au registre des modems.
func (l *Line) Model() modem.Model {
	return modem.Model{ID: ModelID, Name: "Démonstration", Hidden: true,
		New: func(modem.Config) (modem.Driver, error) { return l, nil }}
}

// day renvoie l'instant d'un jour relatif a l'origine, a l'heure locale dite.
func (l *Line) day(d, hour, minute int) time.Time {
	o := l.Origin.In(time.Local)
	return time.Date(o.Year(), o.Month(), o.Day(), hour, minute, 0, 0, time.Local).AddDate(0, 0, d).UTC()
}

type span struct{ from, to time.Time }

func (s span) has(t time.Time) bool { return !t.Before(s.from) && t.Before(s.to) }

// Le scenario.
func (l *Line) degraded() span        { return span{l.day(-62, 7, 0), l.day(-52, 10, 30)} }
func (l *Line) technician() time.Time { return l.day(-52, 10, 0) }
func (l *Line) spike() span           { return span{l.day(-6, 19, 0), l.day(-6, 21, 0)} }

// scriptedRestart est le redemarrage de la coupure de nuit.
func (l *Line) scriptedRestart() time.Time { return l.day(-20, 3, 12) }

// outages sont les coupures de la sonde.
func (l *Line) outages() []struct {
	kind string
	span span
} {
	return []struct {
		kind string
		span span
	}{
		{probe.KindLine, span{l.day(-58, 20, 5), l.day(-58, 21, 50)}},
		{probe.KindLine, span{l.day(-35, 21, 40), l.day(-35, 21, 52)}},
		{probe.KindLine, span{l.day(-20, 2, 55), l.day(-20, 3, 20)}},
		{probe.KindLocal, span{l.day(-3, 8, 10), l.day(-3, 8, 50)}},
	}
}

func (l *Line) down(t time.Time) bool {
	for _, o := range l.outages() {
		if o.span.has(t) {
			return true
		}
	}
	return false
}

// lastRestart est le dernier demarrage du modem avant t.
func (l *Line) lastRestart(t time.Time) time.Time {
	last := l.Origin.AddDate(0, 0, -120)
	if r := l.scriptedRestart(); !r.After(t) {
		last = r
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, r := range l.restarts {
		if !r.After(t) && r.After(last) {
			last = r
		}
	}
	return last
}

// noise rend une valeur pseudo-aleatoire dans [-1, 1], stable pour un
// instant (a dix minutes pres) et une cle.
func noise(t time.Time, key int) float64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "%d/%d", t.Unix()/600, key)
	return float64(h.Sum64()%2001)/1000 - 1
}

// daily est le cycle de la journee, de -1 a 1, au plus haut en fin
// d'apres-midi : la temperature du cable.
func daily(t time.Time) float64 {
	h := float64(t.In(time.Local).Hour()) + float64(t.Minute())/60
	return math.Sin(2 * math.Pi * (h - 11) / 24)
}

// evening dit si t tombe aux heures chargees, de 19 a 23 heures.
func evening(t time.Time) bool {
	h := t.In(time.Local).Hour()
	return h >= 19 && h < 23
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

const dsChannels = 16

// At rend le releve de l'instant t.
func (l *Line) At(t time.Time) docsis.RawSnapshot {
	snap := docsis.RawSnapshot{DOCSIS: "3.1"}
	since := t.Sub(l.lastRestart(t)).Minutes()
	bad := l.degraded()
	for i := 0; i < dsChannels; i++ {
		id := i + 1
		freq := 442 + 8*i
		if i >= 4 {
			freq += 8 // le canal 474 MHz n'est pas utilise
		}
		power := 1.4 + 0.29*float64(i) + 0.35*daily(t) + 0.15*noise(t, id)
		snr := 40.6 + 0.3*noise(t, 100+id) - 0.1*daily(t)
		corr := int64(since * float64(3+id%4))
		uncorr := int64(since / 240)
		if bad.has(t) && id >= 9 {
			// Connecteur oxyde : les canaux hauts chutent et perdent du SNR.
			power = -8.6 - 0.12*float64(id-9) + 0.1*noise(t, id)
			snr -= 6
		}
		if overlap := overlapMinutes(bad, l.lastRestart(t), t); overlap > 0 && id >= 9 {
			uncorr += int64(overlap * 0.4)
		}
		if id == 5 {
			if overlap := overlapMinutes(l.spike(), l.lastRestart(t), t); overlap > 0 {
				uncorr += int64(overlap * 120)
			}
		}
		snap.Downstream = append(snap.Downstream, docsis.RawDownstream{
			ChannelID: docsis.FlexInt(id), Frequency: fmt.Sprintf("%d MHz", freq), Modulation: "256QAM",
			PowerLevel: fmt.Sprintf("%g", round1(power)), MSE: fmt.Sprintf("%g", -round1(snr)),
			CorrErrors: docsis.FlexInt(corr), NonCorrErrors: docsis.FlexInt(uncorr),
		})
	}
	ofdmPower := 5.8 + 0.3*daily(t) + 0.1*noise(t, 159)
	if bad.has(t) {
		ofdmPower = -9.1 + 0.1*noise(t, 159)
	}
	snap.Downstream = append(snap.Downstream, docsis.RawDownstream{
		ChannelID: 159, Frequency: "706.5 MHz", Modulation: "OFDM",
		PowerLevel: fmt.Sprintf("%g", round1(ofdmPower)), MSE: fmt.Sprintf("%g", -round1(43.4+0.2*noise(t, 160))),
		CorrErrors: docsis.FlexInt(int64(since * 40)), NonCorrErrors: 0,
	})
	for i, f := range []string{"37.2", "44.2", "50.7", "57.2"} {
		power := 35.5 + 0.5*float64(i%2) - 0.3*daily(t) + 0.1*noise(t, 200+i)
		if bad.has(t) {
			power += 14.6
		}
		snap.Upstream = append(snap.Upstream, docsis.RawUpstream{
			ChannelID: docsis.FlexInt(i + 1), Frequency: f + " MHz", Modulation: "64QAM",
			PowerLevel: fmt.Sprintf("%g", round1(power)),
		})
	}
	usOFDMA := 36.0 - 0.3*daily(t)
	if bad.has(t) {
		usOFDMA += 14
	}
	snap.Upstream = append(snap.Upstream, docsis.RawUpstream{
		ChannelID: 9, Frequency: "138 MHz", Modulation: "OFDMA", Multiplex: "2K",
		PowerLevel: fmt.Sprintf("%g", round1(usOFDMA)),
	})
	return snap
}

// overlapMinutes est la duree, en minutes, de s comprise entre from et to.
func overlapMinutes(s span, from, to time.Time) float64 {
	a, b := s.from, s.to
	if a.Before(from) {
		a = from
	}
	if b.After(to) {
		b = to
	}
	if !b.After(a) {
		return 0
	}
	return b.Sub(a).Minutes()
}

// Snapshot rend le releve du moment : c'est le pilote de la collecte.
func (l *Line) Snapshot(context.Context) (docsis.RawSnapshot, error) {
	return l.At(l.Now()), nil
}

// Info decrit le modem fictif, en mode pont.
func (l *Line) Info(context.Context) (modem.Info, error) {
	now := l.Now()
	return modem.Info{Model: "Démonstration", Uptime: now.Sub(l.lastRestart(now)).Round(time.Second), Mode: modem.ModeBridge,
		Status: "OPERATIONAL"}, nil
}

// Restart redemarre le modem fictif : ses compteurs repartent de zero.
func (l *Line) Restart(context.Context) error {
	l.mu.Lock()
	l.restarts = append(l.restarts, l.Now().UTC())
	l.mu.Unlock()
	return nil
}

// speed rend une mesure de debit plausible a l'instant t.
func (l *Line) speed(t time.Time) store.SpeedTest {
	down := 948 + 12*noise(t, 300)
	up := 48.5 + 1.2*noise(t, 301)
	ping := 11.5 + 1.5*noise(t, 302)
	if evening(t) {
		down -= 180 + 60*noise(t, 303)
		ping += 6
	}
	if l.degraded().has(t) {
		down, up = 610+40*noise(t, 304), 31+3*noise(t, 305)
	}
	jitter := round1(1.2 + 0.4*noise(t, 306))
	loss := 0.0
	ping = round1(ping)
	return store.SpeedTest{Time: t, Download: round1(down), Upload: round1(up), Ping: &ping, Jitter: &jitter,
		Loss: &loss, Server: "Orange Belgium (Bruxelles)"}
}

// Fill ecrit l'historique : releves et journal par les chemins de la
// collecte, sonde, coupures, debit et une note.
func (l *Line) Fill(ctx context.Context, db *sql.DB, rec *events.Recorder) error {
	start := l.Origin.AddDate(0, 0, -Days).Truncate(10 * time.Minute)
	recent := l.Origin.AddDate(0, 0, -14)
	for t := start; t.Before(l.Origin); {
		snap := l.At(t).Normalize(t)
		if _, err := store.SaveSnapshot(ctx, db, snap); err != nil {
			return err
		}
		rec.Recorded(ctx, snap)
		// Un releve par heure dans l'ancien historique, toutes les vingt
		// minutes ensuite : l'historique s'ecrit en quelques secondes.
		if t.Before(recent) {
			t = t.Add(time.Hour)
		} else {
			t = t.Add(20 * time.Minute)
		}
	}

	// Sonde : une tranche de cinq minutes, dix tentatives.
	var samples []store.ProbeSample
	for t := start; t.Before(l.Origin); t = t.Add(5 * time.Minute) {
		s := store.ProbeSample{Start: t, Rounds: 10}
		if l.down(t) {
			s.Failed = 10
		} else {
			avg := 11.8 + 0.8*noise(t, 400)
			if evening(t) {
				avg += 5.5 + 1.5*noise(t, 401)
			}
			lo, hi := avg-2.1, avg+4.2+2*math.Abs(noise(t, 402))
			s.RTTMin, s.RTTAvg, s.RTTMax = &lo, &avg, &hi
		}
		samples = append(samples, s)
	}
	if err := store.SaveProbeSamples(ctx, db, samples); err != nil {
		return err
	}
	what := map[string]string{probe.KindLine: "de la ligne", probe.KindLocal: "du réseau local"}
	for _, o := range l.outages() {
		msg, level := "Coupure de la ligne : Internet ne répond plus, le modem si.", analyze.Critical
		if o.kind == probe.KindLocal {
			msg, level = "Réseau local coupé : ni Internet ni le modem ne répondent.", analyze.Warning
		}
		id, err := store.OpenOutage(ctx, db, o.kind, o.span.from, msg)
		if err != nil {
			return err
		}
		if err := store.CloseOutage(ctx, db, id, o.span.to); err != nil {
			return err
		}
		if err := store.SaveEvents(ctx, db, []store.Event{
			{Time: o.span.from, Kind: events.KindOutage, Level: int(level), Message: msg},
			{Time: o.span.to, Kind: events.KindOutageEnd, Level: int(analyze.Good),
				Message: "Fin de coupure " + what[o.kind] + ", après " + events.Duration(o.span.to.Sub(o.span.from)) + "."},
		}); err != nil {
			return err
		}
	}

	// Debit : une mesure toutes les deux heures, sauf pendant les coupures.
	var tests []store.SpeedTest
	for t := start.Add(17 * time.Minute); t.Before(l.Origin); t = t.Add(2 * time.Hour) {
		if !l.down(t) {
			tests = append(tests, l.speed(t))
		}
	}
	if _, err := store.SaveSpeedTests(ctx, db, tests); err != nil {
		return err
	}

	if err := store.AddNote(ctx, db, l.technician(), "Passage du technicien : connecteur oxydé remplacé dans la boîte de raccordement."); err != nil {
		return err
	}
	return auth.SetPassword(ctx, db, Password)
}
