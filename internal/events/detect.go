// Package events tient le journal de la ligne : chaque releve est compare au
// precedent, et ce qui a change devient un evenement date.
package events

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/operator"
	"github.com/floopich/mire/internal/store"
)

// Types d'evenements.
const (
	KindState   = "etat"
	KindRestart = "redemarrage"
	// KindRestartAsked est un redemarrage demande depuis la page Modem.
	KindRestartAsked = "redemarrage_demande"
	// KindRestartMissed : le modem a accepte la demande, mais ne s'est pas
	// arrete, son temps de fonctionnement en temoigne.
	KindRestartMissed = "redemarrage_absent"
	KindChannelsLost  = "canaux_perdus"
	KindChannelsBack  = "canaux_retrouves"
	KindModulation    = "modulation"
	KindErrors        = "erreurs"
	KindErrorsEnd     = "erreurs_fin"
	KindModemDown     = "modem_injoignable"
	KindModemUp       = "modem_retrouve"
	KindOutage        = "coupure"
	KindOutageEnd     = "coupure_fin"
	directionDown     = "descendant"
	directionUp       = "montant"
	restartMinOverlap = 3
	// restartShare est la part des canaux comparables dont les compteurs
	// doivent baisser pour conclure a un redemarrage : un canal isole remis a
	// zero ne suffit pas.
	restartShare = 0.8
)

// Input rassemble ce que la detection compare.
type Input struct {
	// Prev est le releve precedent, nil au tout premier.
	Prev *docsis.Snapshot
	Cur  docsis.Snapshot
	// Health est le verdict porte sur Cur.
	Health analyze.Health
	// LastState est le dernier changement d'etat inscrit, nil s'il n'y en a
	// pas : la ligne est alors reputee en bon etat.
	LastState *store.Event
	// OpenSpike est le debut du pic d'erreurs en cours, nil s'il n'y en a
	// pas ; SpikeTotal compte alors les erreurs non corrigees depuis son
	// debut, releve courant compris.
	OpenSpike  *store.Event
	SpikeTotal int64
	Profile    operator.Profile
}

// Detect deduit les evenements d'un releve.
func Detect(in Input) []store.Event {
	var out []store.Event
	add := func(e store.Event) {
		e.Time = in.Cur.Timestamp
		out = append(out, e)
	}
	if in.Prev != nil {
		restarted := restart(*in.Prev, in.Cur)
		if restarted {
			add(store.Event{Kind: KindRestart, Level: int(analyze.Warning),
				Message: "Modem redémarré : compteurs remis à zéro."})
		}
		for _, e := range channelChanges(*in.Prev, in.Cur) {
			add(e)
		}
		for _, e := range modulationChanges(*in.Prev, in.Cur) {
			add(e)
		}
		for _, e := range powerChanges(*in.Prev, in.Cur) {
			add(e)
		}
		// Un pic qui dure plusieurs releves est un seul episode : un debut,
		// puis une fin qui en donne le total.
		if in.Profile.Calibrated {
			spike := uncorrectedDelta(*in.Prev, in.Cur) >= in.Profile.Thresholds.Errors.Spike()
			switch {
			case spike && in.OpenSpike == nil:
				add(store.Event{Kind: KindErrors, Level: int(analyze.Warning),
					Message: fmt.Sprintf("Pic d'erreurs : %s non corrigées depuis le relevé précédent.",
						count(uncorrectedDelta(*in.Prev, in.Cur)))})
			case !spike && in.OpenSpike != nil:
				add(store.Event{Kind: KindErrorsEnd, Level: int(analyze.Good),
					Message: fmt.Sprintf("Fin du pic d'erreurs : %s non corrigées en %s.",
						count(in.SpikeTotal), Duration(in.Cur.Timestamp.Sub(in.OpenSpike.Time)))})
			}
		}
	}
	if e, ok := stateChange(in); ok {
		add(e)
	}
	return out
}

// alert ramene un niveau aux trois etats que le journal distingue : un canal
// tolere n'est pas une alerte.
func alert(l analyze.Level) analyze.Level {
	if l == analyze.Tolerated {
		return analyze.Good
	}
	return l
}

func stateChange(in Input) (store.Event, bool) {
	if !in.Health.Qualified {
		return store.Event{}, false
	}
	cur := alert(in.Health.Level)
	if cur == analyze.Unknown {
		return store.Event{}, false
	}
	last := analyze.Good
	if in.LastState != nil {
		last = alert(analyze.Level(in.LastState.Level))
	}
	if cur == last {
		return store.Event{}, false
	}
	e := store.Event{Kind: KindState, Level: int(cur)}
	switch cur {
	case analyze.Good:
		e.Message = "Ligne revenue en bon état."
		return e, true
	case analyze.Warning:
		e.Message = "Ligne à surveiller : "
	default:
		e.Message = "Ligne critique : "
	}
	cause, dir, id := cause(in.Health, cur)
	e.Message += cause + "."
	e.Direction, e.ChannelID = dir, id
	return e, true
}

// Diagnosis decrit l'etat de la ligne en une phrase, comme le journal :
// « Ligne critique : canal descendant 159, puissance descendante 26,6 dBmV,
// hors limites. » Un profil non calibre ne juge pas la ligne.
func Diagnosis(h analyze.Health) string {
	if !h.Qualified {
		return "Ligne non jugée : le profil d'opérateur n'a pas de seuils."
	}
	switch alert(h.Level) {
	case analyze.Good:
		return "Ligne en bon état."
	case analyze.Warning:
		text, _, _ := cause(h, analyze.Warning)
		return "Ligne à surveiller : " + text + "."
	case analyze.Critical:
		text, _, _ := cause(h, analyze.Critical)
		return "Ligne critique : " + text + "."
	default:
		return "Aucune mesure exploitable dans le dernier relevé."
	}
}

// cause decrit le premier canal au niveau donne, et signale les autres.
// Elle renvoie le canal quand il est seul en cause.
func cause(h analyze.Health, level analyze.Level) (string, string, int) {
	type hit struct {
		dir string
		ch  analyze.ChannelHealth
	}
	var hits []hit
	for _, c := range h.Downstream {
		if c.Level == level {
			hits = append(hits, hit{directionDown, c})
		}
	}
	for _, c := range h.Upstream {
		if c.Level == level {
			hits = append(hits, hit{directionUp, c})
		}
	}
	if len(hits) == 0 {
		return "état dégradé", "", 0
	}
	first := hits[0]
	text := "canal " + first.dir + " " + strconv.Itoa(first.ch.ChannelID)
	for _, f := range first.ch.Findings {
		if f.Level == level {
			text += ", " + f.Metric + " " + f.Message
			break
		}
	}
	switch n := len(hits) - 1; {
	case n == 1:
		text += ", et 1 autre canal"
	case n > 1:
		text += ", et " + strconv.Itoa(n) + " autres canaux"
	}
	if len(hits) == 1 {
		return text, first.dir, first.ch.ChannelID
	}
	return text, "", 0
}

// restart reconnait un redemarrage du modem a la baisse de ses compteurs
// d'erreurs sur presque tous les canaux.
func restart(prev, cur docsis.Snapshot) bool {
	before := map[int]docsis.Downstream{}
	for _, c := range prev.Downstream {
		before[c.ChannelID] = c
	}
	comparable, declining := 0, 0
	for _, c := range cur.Downstream {
		p, ok := before[c.ChannelID]
		if !ok {
			continue
		}
		comparable++
		down := c.CorrErrors < p.CorrErrors || c.NonCorrErrors < p.NonCorrErrors
		up := c.CorrErrors > p.CorrErrors || c.NonCorrErrors > p.NonCorrErrors
		if down && !up {
			declining++
		}
	}
	return comparable >= restartMinOverlap && float64(declining) >= restartShare*float64(comparable)
}

func uncorrectedDelta(prev, cur docsis.Snapshot) int64 {
	before := map[int]int64{}
	for _, c := range prev.Downstream {
		before[c.ChannelID] = c.NonCorrErrors
	}
	var n int64
	for _, c := range cur.Downstream {
		if p, ok := before[c.ChannelID]; ok {
			n += docsis.CounterDelta(p, c.NonCorrErrors)
		}
	}
	return n
}

func channelChanges(prev, cur docsis.Snapshot) []store.Event {
	var out []store.Event
	diff := func(dir string, before, after []int) {
		lost, back := setDiff(before, after), setDiff(after, before)
		if len(lost) > 0 {
			out = append(out, channelEvent(KindChannelsLost, dir, lost, analyze.Warning, "perdu"))
		}
		if len(back) > 0 {
			out = append(out, channelEvent(KindChannelsBack, dir, back, analyze.Good, "retrouvé"))
		}
	}
	diff(directionDown, downIDs(prev), downIDs(cur))
	diff(directionUp, upIDs(prev), upIDs(cur))
	return out
}

func channelEvent(kind, dir string, ids []int, level analyze.Level, verb string) store.Event {
	e := store.Event{Kind: kind, Level: int(level), Direction: dir}
	if len(ids) == 1 {
		e.ChannelID = ids[0]
		e.Message = fmt.Sprintf("Canal %s %d %s.", dir, ids[0], verb)
		return e
	}
	e.Message = fmt.Sprintf("Canaux %ss %ss : %s.", dir, verb, list(ids))
	return e
}

type modChange struct {
	id       int
	from, to string
	down     bool
}

func modulationChanges(prev, cur docsis.Snapshot) []store.Event {
	var out []store.Event
	emit := func(dir string, changes []modChange) {
		var lower, higher []modChange
		for _, c := range changes {
			if c.down {
				lower = append(lower, c)
			} else {
				higher = append(higher, c)
			}
		}
		if len(lower) > 0 {
			out = append(out, modEvent(dir, lower, analyze.Warning, "Modulation en baisse"))
		}
		if len(higher) > 0 {
			out = append(out, modEvent(dir, higher, analyze.Good, "Modulation en hausse"))
		}
	}

	before := map[int]string{}
	for _, c := range prev.Downstream {
		before[c.ChannelID] = c.Modulation
	}
	var ds []modChange
	for _, c := range cur.Downstream {
		if mc, ok := compare(c.ChannelID, before, c.Modulation); ok {
			ds = append(ds, mc)
		}
	}
	emit(directionDown, ds)

	before = map[int]string{}
	for _, c := range prev.Upstream {
		before[c.ChannelID] = upLabel(c)
	}
	var us []modChange
	for _, c := range cur.Upstream {
		if mc, ok := compare(c.ChannelID, before, upLabel(c)); ok {
			us = append(us, mc)
		}
	}
	emit(directionUp, us)
	return out
}

// upLabel donne la modulation effective d'un canal montant : un canal OFDMA
// porte l'ordre de son profil actif dans Multiplex.
func upLabel(c docsis.Upstream) string {
	if analyze.QAMOrder(c.Modulation) == 0 && analyze.QAMOrder(c.Multiplex) > 0 {
		return c.Multiplex
	}
	return c.Modulation
}

func compare(id int, before map[int]string, now string) (modChange, bool) {
	was, ok := before[id]
	if !ok {
		return modChange{}, false
	}
	a, b := analyze.QAMOrder(was), analyze.QAMOrder(now)
	if a == 0 || b == 0 || a == b {
		return modChange{}, false
	}
	return modChange{id: id, from: was, to: now, down: b < a}, true
}

func modEvent(dir string, changes []modChange, level analyze.Level, title string) store.Event {
	e := store.Event{Kind: KindModulation, Level: int(level), Direction: dir}
	var parts []string
	for _, c := range changes {
		parts = append(parts, fmt.Sprintf("canal %d (%s → %s)", c.id, c.from, c.to))
	}
	if len(changes) == 1 {
		e.ChannelID = changes[0].id
	}
	e.Message = fmt.Sprintf("%s, voie %se : %s.", title, dir, joinFr(parts)) // voie descendante, montante
	return e
}

func downIDs(s docsis.Snapshot) []int {
	out := make([]int, 0, len(s.Downstream))
	for _, c := range s.Downstream {
		out = append(out, c.ChannelID)
	}
	return out
}

func upIDs(s docsis.Snapshot) []int {
	out := make([]int, 0, len(s.Upstream))
	for _, c := range s.Upstream {
		out = append(out, c.ChannelID)
	}
	return out
}

// setDiff renvoie, tries, les elements de a absents de b.
func setDiff(a, b []int) []int {
	in := map[int]bool{}
	for _, v := range b {
		in[v] = true
	}
	var out []int
	for _, v := range a {
		if !in[v] {
			out = append(out, v)
		}
	}
	sort.Ints(out)
	return out
}

func list(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return joinFr(parts)
}

// joinFr enumere a la francaise : « 3, 7 et 12 ».
func joinFr(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " et " + parts[len(parts)-1]
}

// count rend un entier a la francaise : 12 400.
func count(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + " " + s[i:]
	}
	return s
}
