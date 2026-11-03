package webview

import (
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/operator"
	"github.com/floopich/mire/internal/settings"
	"github.com/floopich/mire/internal/status"
	"github.com/floopich/mire/internal/store"
)

// La page « Comprendre la ligne » explique chaque mesure avec les seuils du
// profil et les valeurs du dernier releve.

type link struct{ Href, Label string }

type learnState struct {
	Level analyze.Level
	Text  string
}

type learnSection struct {
	ID, Title string
	Paras     []string
	// Yours est la valeur du dernier releve, vide sans releve.
	Yours  string
	Link   *link
	States []learnState
	Steps  []string
	// Table liste les seuils du profil, mesure par mesure.
	Table []thresholdRow
	// Source est la reference des seuils, Note le detail du profil.
	Source *link
	Note   string
}

// thresholdRow est une ligne du tableau des seuils.
type thresholdRow struct {
	Measure, Good, Warning, Critical string
}

type learnView struct {
	common
	Sections []learnSection
}

func (s *Server) handleLearn(w http.ResponseWriter, r *http.Request) {
	st := s.opts.Service.State()
	line, err := s.readStatus(r.Context(), st)
	if err != nil {
		s.fail(w, err)
		return
	}
	var errs map[int]store.ErrorCount
	window := st.Profile.Thresholds.Errors.Window()
	if line.HasData {
		end := line.Snapshot.Timestamp
		if errs, err = store.ErrorTotals(r.Context(), s.db, end.Add(-window), end); err != nil {
			s.fail(w, err)
			return
		}
	}
	v := learnView{common: s.commonFor(r, "Comprendre la ligne"),
		Sections: learnSections(st.Profile, st.Interval, line, errs, window)}
	// Les seuils et les limites, avant « Que faire ? ».
	last := v.Sections[len(v.Sections)-1]
	v.Sections = append(append(v.Sections[:len(v.Sections)-1], limitSections(st.Profile,
		analyze.Effective(st.Profile.Thresholds, analyze.Options{OFDMAFloor: st.Values.Int(settings.OFDMAFloor)}))...), last)
	s.render(w, http.StatusOK, "comprendre", v)
}

// learnSections ecrit la page. Sans profil calibre, les seuils sont tus :
// le profil generique ne juge pas la ligne.
func learnSections(p operator.Profile, interval time.Duration, line status.Status, errs map[int]store.ErrorCount, window time.Duration) []learnSection {
	t := p.Thresholds
	sum := line.Summarize()
	name := p.Name
	cal := p.Calibrated
	var out []learnSection

	// La ligne en deux mots.
	sec := learnSection{ID: "ligne", Title: "La ligne en deux mots", Paras: []string{
		"Le signal du câble est découpé en canaux. La voie descendante va de l'opérateur vers vous, la voie montante de vous vers l'opérateur.",
		"Pour chaque canal : puissance, qualité du signal (SNR) et erreurs. Mire les relève toutes les " + every(interval) + " et les compare aux seuils de votre opérateur.",
	}}
	if line.HasData {
		sec.Yours = "Chez vous : " + plural(sum.DSCount, "canal descendant", "canaux descendants") + ", " +
			plural(sum.USCount, "canal montant", "canaux montants") + "."
	}
	out = append(out, sec)

	// Puissance recue.
	sec = learnSection{ID: "puissance", Title: "Puissance reçue", Paras: []string{
		"En dBmV. Trop faible ou trop forte, elle provoque des erreurs.",
	}}
	if b, ok := t.DownstreamPowerFor(t.DownstreamDefault); ok && cal {
		sec.Paras = append(sec.Paras, "Pour "+name+", la plage normale va "+rangeText(b.Good, "dBmV")+
			" ; "+limitText(b.Warning, "dBmV")+", c'est à surveiller ; au-delà, critique.")
	}
	sec.Paras = append(sec.Paras, "Un écart brusque sur tous les canaux vient souvent de l'installation : connecteur, répartiteur, câble.")
	if sum.DSPowerMin != nil {
		sec.Yours = "Chez vous : de " + Fmt(sum.DSPowerMin, "dBmV") + " à " + Fmt(sum.DSPowerMax, "dBmV") + "."
	}
	out = append(out, sec)

	// Emission.
	sec = learnSection{ID: "emission", Title: "Émission", Paras: []string{
		"Puissance d'émission du modem vers l'opérateur. Elle monte quand le retour perd du signal.",
	}}
	if b, ok := t.UpstreamPowerFor(t.UpstreamDefault); ok && cal {
		sec.Paras = append(sec.Paras, "Pour "+name+", "+rangeText(b.Good, "dBmV")+" est normal ; jusqu'à "+
			short(b.Warning.Max)+" dBmV, c'est à surveiller ; au-delà, critique : vers "+short(b.Critical.Max)+
			" dBmV, des canaux tombent.")
	}
	if sum.USPowerMax != nil {
		sec.Yours = "Chez vous : " + Fmt(sum.USPowerMax, "dBmV") + " au plus."
	}
	out = append(out, sec)

	// SNR.
	sec = learnSection{ID: "snr", Title: "Qualité du signal : le SNR", Paras: []string{
		"Rapport signal/bruit, en dB. Plus il est élevé, mieux c'est.",
	}}
	if b, ok := t.SNRFor("256QAM"); ok && cal {
		p := "Pour un canal 256QAM chez " +
			name + ", " + short(b.GoodMin) + " dB et plus est bon, sous " + short(b.GoodMin) + " dB à surveiller, sous " +
			short(b.WarningMin) + " dB critique."
		if o, ok := t.SNR["ofdm"]; ok {
			p += " Un canal OFDM est bon dès " + short(o.GoodMin) + " dB."
		}
		sec.Paras = append(sec.Paras, p)
	}
	if sum.DSSNRMin != nil {
		sec.Yours = "Chez vous : " + Fmt(sum.DSSNRMin, "dB") + " au plus bas."
	}
	out = append(out, sec)

	// Familles de canaux.
	sec = learnSection{ID: "familles", Title: "SC-QAM, OFDM et OFDMA", Paras: []string{
		"SC-QAM : canaux DOCSIS 3.0, de 6 ou 8 MHz. OFDM (descendant) et OFDMA (montant) : canaux DOCSIS 3.1, bien plus larges.",
		"Une puissance OFDM sans rapport avec les canaux voisins, alors que la réception est excellente, est signalée comme mesure douteuse : un redémarrage du modem la corrige souvent.",
	}}
	if line.HasData {
		sec.Yours = "Chez vous : " + families(line.Snapshot) + "."
	}
	out = append(out, sec)

	// Modulation.
	sec = learnSection{ID: "modulation", Title: "Modulation", Paras: []string{
		"16QAM, 64QAM, 256QAM… Elle baisse quand le signal se dégrade, au prix du débit.",
		"Certains segments restent en basse modulation montante : déclarez-la dans les réglages pour qu'elle serve de référence.",
	}, Link: &link{"/reglages/analyse", "Régler la modulation habituelle"}}
	if m := upstreamModulations(line.Snapshot); m != "" {
		sec.Yours = "Chez vous : voie montante en " + m + "."
	}
	out = append(out, sec)

	// Erreurs.
	sec = learnSection{ID: "erreurs", Title: "Erreurs corrigées et non corrigées", Paras: []string{
		"Les erreurs corrigées sont normales. Les erreurs non corrigées sont des données perdues.",
	}}
	if e := t.Errors; cal && e.UncorrectablePctWarning > 0 {
		sec.Paras = append(sec.Paras, "Mire juge les dernières "+
			hours(window)+". Pour "+
			name+", un canal est à surveiller à partir de "+FmtCount(int(e.Spike()))+" erreurs non corrigées sur la période et "+
			short(e.UncorrectablePctWarning)+" % de ses erreurs, critique à partir de "+short(e.UncorrectablePctCritical)+" %.")
	}
	if errs != nil {
		var c, u int64
		for _, e := range errs {
			c += e.Corrected
			u += e.Uncorrected
		}
		sec.Yours = "Chez vous, sur " + hours(window) + " : " + FmtCount(int(c)) + " corrigées, " + FmtCount(int(u)) + " non corrigées."
	}
	out = append(out, sec)

	// Etats.
	sec = learnSection{ID: "etats", Title: "Les états", States: []learnState{
		{analyze.Good, "Dans la plage normale de l'opérateur."},
		{analyze.Tolerated, "Un cran sous l'idéal, sans effet sur la connexion."},
		{analyze.Warning, "Hors de la plage normale, ou mesure douteuse."},
		{analyze.Critical, "Coupures, lenteurs ou pertes probables."},
		{analyze.Unknown, "Pas de mesure, ou un profil qui ne juge pas la ligne."},
	}}
	if cal {
		sec.Paras = []string{"Les seuils viennent du profil d'opérateur : voir plus bas d'où ils viennent."}
	} else {
		sec.Paras = []string{"Le profil « " + name + " » ne juge pas la ligne : les mesures sont relevées sans seuils."}
	}
	out = append(out, sec)

	// Que faire.
	out = append(out, learnSection{ID: "agir", Title: "Que faire ?", Steps: []string{
		"Vérifiez l'installation : connecteurs serrés, pas de répartiteur inutile, câble en bon état.",
		"Une seule mesure aberrante : redémarrez le modem.",
		"Un problème intermittent se voit après quelques jours de mesures.",
		"Contactez l'opérateur avec le rapport de la ligne.",
		"Ne touchez ni au boîtier de l'opérateur ni à l'amplificateur d'immeuble.",
	}})
	return out
}

// rangeText rend un intervalle : « de −8 à +8 dBmV ».
func rangeText(r operator.Range, unit string) string {
	lo, hi := short(r.Min), short(r.Max)
	if r.Min < 0 && r.Max > 0 {
		hi = "+" + hi
	}
	return "de " + lo + " à " + hi + nbsp + unit
}

// limitText rend une borne d'alerte : « jusqu'à ±10 dBmV ».
func limitText(r operator.Range, unit string) string {
	if r.Min == -r.Max {
		return "jusqu'à ±" + short(r.Max) + nbsp + unit
	}
	return rangeText(r, unit)
}

// short rend un seuil sans decimale inutile : 8, 0,5, −8.
func short(v float64) string {
	if v == math.Trunc(v) {
		return strings.Replace(strconv.Itoa(int(v)), "-", "−", 1)
	}
	return dec(v)
}

// families compte les canaux par famille : « 16 SC-QAM et 1 OFDM en voie
// descendante, 4 SC-QAM et 1 OFDMA en voie montante ».
func families(snap docsis.Snapshot) string {
	var dsSC, dsOFDM, usSC, usOFDMA int
	for _, c := range snap.Downstream {
		if strings.EqualFold(c.Modulation, "OFDM") {
			dsOFDM++
		} else {
			dsSC++
		}
	}
	for _, c := range snap.Upstream {
		if strings.EqualFold(c.Modulation, "OFDMA") {
			usOFDMA++
		} else {
			usSC++
		}
	}
	part := func(sc, wide int, wideName, where string) string {
		var p []string
		if sc > 0 {
			p = append(p, strconv.Itoa(sc)+" SC-QAM")
		}
		if wide > 0 {
			p = append(p, strconv.Itoa(wide)+" "+wideName)
		}
		if len(p) == 0 {
			return ""
		}
		return strings.Join(p, " et ") + " en voie " + where
	}
	return strings.TrimPrefix(strings.Join(nonEmpty(part(dsSC, dsOFDM, "OFDM", "descendante"), part(usSC, usOFDMA, "OFDMA", "montante")), ", "), ", ")
}

func nonEmpty(s ...string) []string {
	var out []string
	for _, x := range s {
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}

// upstreamModulations liste les modulations des canaux montants SC-QAM.
func upstreamModulations(snap docsis.Snapshot) string {
	seen := map[string]bool{}
	var out []string
	for _, c := range snap.Upstream {
		m := strings.TrimSpace(c.Modulation)
		if m == "" || strings.EqualFold(m, "OFDMA") || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return analyze.QAMOrder(out[i]) > analyze.QAMOrder(out[j]) })
	return strings.Join(out, ", ")
}

// hours rend une fenetre : « 24 h ».
func hours(d time.Duration) string {
	return strconv.Itoa(int(d.Hours())) + nbsp + "h"
}

// limitSections ecrit le tableau des seuils et ce que Mire ne sait pas
// juger. Sans profil calibre, il n'y a pas de seuils a montrer.
func limitSections(p operator.Profile, t operator.Thresholds) []learnSection {
	var out []learnSection
	if p.Calibrated {
		sec := learnSection{ID: "seuils", Title: "D'où viennent les seuils", Paras: []string{
			"Seuils " + p.Name + " : " + nonBlank(p.Basis, "voir la note du profil") + ".",
		}, Table: thresholdTable(t)}
		if !p.Validated {
			sec.Yours = "À confirmer : ces seuils n'ont pas encore été comparés à des relevés de ce réseau."
		}
		sec.Note = p.Notes
		if p.Source != "" {
			sec.Source = &link{p.Source, "Référence du profil"}
		}
		out = append(out, sec)
	}
	out = append(out, learnSection{ID: "limites", Title: "Ce que Mire ne sait pas juger", Paras: []string{
		"Mire lit ce que le modem publie. Une mesure fausse du modem fausse le verdict : une puissance OFDM sans rapport avec les canaux voisins est signalée comme douteuse, pas comme une panne.",
		"La voie montante ne se juge qu'en partie : la qualité de ce que le modem émet se mesure chez l'opérateur. Un bruit qui entre sur le retour ne se voit qu'à ses signes : émission qui grimpe, modulation qui baisse, erreurs à heures fixes.",
		"Les erreurs non corrigées sont comptées en part des erreurs, pas des données reçues : le modem ne publie pas le nombre de blocs reçus.",
		"Un relevé est un instant. Une dégradation entre deux relevés peut passer inaperçue ; une coupure courte ne se voit qu'avec une sonde à 30 secondes au plus.",
		"« Constaté depuis » se lit dans l'historique conservé, à l'intervalle des relevés près. Avant le début de l'historique, ou après une interruption des relevés, Mire n'en donne qu'une borne : « depuis au moins ».",
		"Les seuils sont ceux d'un réseau, pas de votre segment : une modulation montante basse peut être la règle du segment. Déclarez-la dans les réglages.",
		"Mire ne mesure ni le débit, ni le Wi-Fi, ni les appareils de votre réseau. Il ne remplace pas la mesure d'un technicien.",
	}})
	return out
}

func nonBlank(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// thresholdTable rend les seuils effectivement appliques, dans l'ordre de la
// page.
func thresholdTable(t operator.Thresholds) []thresholdRow {
	var out []thresholdRow
	power := func(label string, table map[string]operator.PowerBands) {
		for _, g := range grouped(table) {
			b := table[g[0]]
			out = append(out, thresholdRow{label + modNames(g), rangeText(b.Good, "dBmV"),
				"entre " + strings.Replace(strings.TrimPrefix(rangeText(b.Warning, "dBmV"), "de "), " à ", " et ", 1), "au-delà"})
		}
	}
	power("Puissance descendante", t.DownstreamPower)
	for _, g := range grouped(t.SNR) {
		b := t.SNR[g[0]]
		out = append(out, thresholdRow{"SNR" + modNames(g), short(b.GoodMin) + nbsp + "dB et plus",
			"de " + short(b.WarningMin) + " à " + short(b.GoodMin) + nbsp + "dB", "sous " + short(b.WarningMin) + nbsp + "dB"})
	}
	power("Puissance montante", t.UpstreamPower)
	mod := func(label string, l operator.ModulationLimits) {
		if l.WarningMaxQAM == 0 {
			return
		}
		good := "au-dessus de " + strconv.Itoa(l.WarningMaxQAM) + "QAM"
		if l.Usual > 0 {
			good = strconv.Itoa(l.Usual) + "QAM, modulation déclarée"
		} else if l.ToleratedMaxQAM > 0 {
			good = "au-dessus de " + strconv.Itoa(l.ToleratedMaxQAM) + "QAM (" + strconv.Itoa(l.ToleratedMaxQAM) + "QAM toléré)"
		}
		out = append(out, thresholdRow{label, good, strconv.Itoa(l.WarningMaxQAM) + "QAM ou moins", strconv.Itoa(l.CriticalMaxQAM) + "QAM ou moins"})
	}
	mod("Modulation montante, SC-QAM", t.UpstreamModulation)
	mod("Modulation montante, OFDMA", t.UpstreamOFDMA)
	if e := t.Errors; e.UncorrectablePctWarning > 0 {
		out = append(out, thresholdRow{"Erreurs non corrigées, sur " + hours(e.Window()),
			"moins de " + short(e.UncorrectablePctWarning) + nbsp + "% ou moins de " + FmtCount(int(e.Spike())) + " non corrigées",
			"dès " + short(e.UncorrectablePctWarning) + nbsp + "%", "dès " + short(e.UncorrectablePctCritical) + nbsp + "%"})
	}
	return out
}

// modNames rend les modulations d'une ligne : « , 256QAM », « , 64QAM à
// 4096QAM et OFDM ».
func modNames(keys []string) string {
	name := func(k string) string {
		switch k {
		case "ofdm":
			return "OFDM"
		case "ofdma":
			return "OFDMA"
		case "sc_qam":
			return "SC-QAM"
		}
		return k
	}
	var qam, other []string
	for _, k := range keys {
		if analyze.QAMOrder(k) > 0 {
			qam = append(qam, k)
		} else {
			other = append(other, name(k))
		}
	}
	var parts []string
	switch len(qam) {
	case 0:
	case 1:
		parts = append(parts, qam[0])
	case 2:
		parts = append(parts, qam[0]+", "+qam[1])
	default:
		parts = append(parts, qam[0]+" à "+qam[len(qam)-1])
	}
	parts = append(parts, other...)
	return ", " + strings.Join(parts, " et ")
}

// grouped regroupe les modulations aux seuils identiques et voisines dans
// l'ordre de la page.
func grouped[T comparable](m map[string]T) [][]string {
	var out [][]string
	for _, k := range sortedKeys(m) {
		if n := len(out); n > 0 && m[out[n-1][0]] == m[k] {
			out[n-1] = append(out[n-1], k)
			continue
		}
		out = append(out, []string{k})
	}
	return out
}

// sortedKeys range les modulations par ordre croissant, OFDM et OFDMA a la
// fin.
func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := analyze.QAMOrder(keys[i]), analyze.QAMOrder(keys[j])
		if a == 0 || b == 0 {
			return a > b || (a == b && keys[i] > keys[j])
		}
		return a < b
	})
	return keys
}
