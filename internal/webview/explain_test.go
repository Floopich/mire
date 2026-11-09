package webview

import (
	"context"
	"database/sql"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/docsis"
	"github.com/floopich/mire/internal/modem"
	"github.com/floopich/mire/internal/operator"
	"github.com/floopich/mire/internal/store"
)

// baseDegradee : dix releves a dix minutes d'intervalle. Le SNR du canal 1
// passe sous le seuil aux quatre derniers ; l'emission du canal 5 est trop
// forte depuis le premier.
func baseDegradee(t *testing.T, now time.Time) *storeDB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for i := range 10 {
		mse := "-38.9"
		if i >= 6 {
			mse = "-33.5"
		}
		raw := docsis.RawSnapshot{
			DOCSIS:     "3.1",
			Downstream: []docsis.RawDownstream{{ChannelID: 1, Modulation: "256QAM", Frequency: "650 MHz", PowerLevel: "1.5", MSE: mse}},
			Upstream:   []docsis.RawUpstream{{ChannelID: 5, PowerLevel: "50.5"}},
		}
		at := now.Add(-time.Duration(9-i) * 10 * time.Minute)
		if _, err := store.SaveSnapshot(context.Background(), db, raw.Normalize(at)); err != nil {
			t.Fatal(err)
		}
	}
	return &storeDB{db}
}

func TestVerdictExplique(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	b := baseDegradee(t, now)
	h, _ := monter(t, b.DB, essai{Collecting: true, Interval: 10 * time.Minute, Now: func() time.Time { return now }})
	tok := session(t, b.DB)

	body := html.UnescapeString(get(t, h, tok, "/").Body.String())
	debutSNR := now.Add(-30 * time.Minute)
	debutEmission := now.Add(-90 * time.Minute)
	for _, voulu := range []string{
		"Pourquoi ce verdict",
		"Canal descendant 1 (650\u00a0MHz)</a> · SNR : 33,5 dB, sous le seuil nominal",
		"Seuil : nominal dès 35 dB, critique sous 33 dB, en 256QAM.",
		// La suite hors plage commence apres un releve sain : la date est sure.
		"Constaté depuis 30\u00a0min, le " + FmtTime(debutSNR) + ".",
		"Canal montant 5</a> · Puissance montante : 50,5 dBmV, hors de la plage nominale",
		"Seuil : plage nominale de 28 à 49 dBmV, limites de 25 à 52 dBmV, en SC-QAM.",
		// Hors plage des le premier releve conserve : une borne seulement.
		"Constaté depuis au moins 1\u00a0h\u00a030, le " + FmtTime(debutEmission) + ".",
		"Seuils VOO / Orange : valeurs de terrain du réseau VOO",
	} {
		if !strings.Contains(body, voulu) {
			t.Errorf("vue d'ensemble, absent : %s", voulu)
		}
	}

	body = html.UnescapeString(get(t, h, tok, "/descendant/1").Body.String())
	for _, voulu := range []string{
		"État au dernier relevé",
		"Puissance descendante : 1,5 dBmV, dans la plage nominale",
		"Seuil : plage nominale de −8 à +8 dBmV, limites de −15 à +15 dBmV, en 256QAM.",
		"Constaté depuis 30\u00a0min",
	} {
		if !strings.Contains(body, voulu) {
			t.Errorf("page du canal, absent : %s", voulu)
		}
	}
	// Une mesure dans la plage n'est pas datee.
	if strings.Count(body, "Constaté") != 1 {
		t.Error("seule la mesure hors plage est datée")
	}
}

func TestDebutDUnConstat(t *testing.T) {
	t0 := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	at := func(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }
	W, G := analyze.Warning, analyze.Good
	for _, c := range []struct {
		nom     string
		levels  []analyze.Level // du plus recent au plus ancien, toutes les 5 min
		gapAt   int             // indice du releve precede d'un trou d'une heure, -1 sans trou
		start   time.Time
		atLeast bool
	}{
		{"suite puis releve sain", []analyze.Level{W, W, W, G, W}, -1, at(-10), false},
		{"dernier releve seul", []analyze.Level{W, G}, -1, at(0), false},
		{"historique epuise", []analyze.Level{W, W}, -1, at(-5), true},
		{"trou dans les releves", []analyze.Level{W, W, W, W}, 2, at(-5), true},
		{"critique puis avertissement : meme constat", []analyze.Level{analyze.Critical, W, G}, -1, at(-5), false},
	} {
		o := onsetScan{maxGap: 15 * time.Minute}
		ts := t0
		for i, lv := range c.levels {
			if i == c.gapAt {
				ts = ts.Add(-time.Hour)
			}
			if !o.step(ts, lv, true) {
				break
			}
			ts = ts.Add(-5 * time.Minute)
		}
		o.finish()
		if !o.start.Equal(c.start) || o.atLeast != c.atLeast {
			t.Errorf("%s : début %v (borne %v), attendu %v (borne %v)", c.nom, o.start, o.atLeast, c.start, c.atLeast)
		}
	}
	// Une mesure absente arrete la suite : on ne date pas ce qu'on n'a pas vu.
	o := onsetScan{maxGap: 15 * time.Minute}
	o.step(at(0), W, true)
	o.step(at(-5), analyze.Unknown, false)
	o.finish()
	if !o.start.Equal(at(0)) || o.atLeast {
		t.Errorf("mesure absente : %v %v", o.start, o.atLeast)
	}
}

type storeDB struct{ DB *sql.DB }

func TestSeuilsEtLimitesExpliques(t *testing.T) {
	h, cookie := serveurAvec(t, 3, essai{Collecting: true})
	body := html.UnescapeString(get(t, h, cookie, "/comprendre").Body.String())
	for _, voulu := range []string{
		`id="seuils"`, `id="limites"`,
		"<th scope=\"row\">Puissance descendante, 64QAM à 4096QAM et OFDM</th><td data-label=\"Nominal\">de −8 à +8 dBmV</td><td data-label=\"À surveiller\">entre −10 et +10 dBmV</td><td data-label=\"Critique\">au-delà</td>",
		"<th scope=\"row\">SNR, 256QAM</th><td data-label=\"Nominal\">35 dB et plus</td><td data-label=\"À surveiller\">de 33 à 35 dB</td><td data-label=\"Critique\">sous 33 dB</td>",
		"<th scope=\"row\">Modulation montante, OFDMA</th><td data-label=\"Nominal\">au-dessus de 128QAM (128QAM toléré)</td><td data-label=\"À surveiller\">64QAM ou moins</td><td data-label=\"Critique\">32QAM ou moins</td>",
		"« Constaté depuis » se lit dans l'historique conservé",
	} {
		if !strings.Contains(body, voulu) {
			t.Errorf("absent : %s", voulu)
		}
	}
	// Telenet : des references, pas des seuils verifies sur le reseau.
	if !strings.Contains(body, "Seuils VOO / Orange") || strings.Contains(body, "À confirmer") {
		t.Error("profil VOO : seuils de terrain, sans réserve")
	}
}

func TestSeuilsTelenetAConfirmer(t *testing.T) {
	p, err := operator.Load("telenet")
	if err != nil {
		t.Fatal(err)
	}
	secs := limitSections(p, p.Thresholds)
	if secs[0].ID != "seuils" || !strings.HasPrefix(secs[0].Yours, "À confirmer") {
		t.Errorf("Telenet : %+v", secs[0])
	}
	g, err := operator.Load("generique")
	if err != nil {
		t.Fatal(err)
	}
	if secs := limitSections(g, g.Thresholds); len(secs) != 1 || secs[0].ID != "limites" {
		t.Error("profil non calibré : pas de seuils à montrer")
	}
	if basisText(g) != "" {
		t.Error("profil non calibré : pas de source de seuils")
	}
}

func TestRapportDitLesSeuils(t *testing.T) {
	h, cookie := serveurAvec(t, 3, essai{Collecting: true})
	body := html.UnescapeString(get(t, h, cookie, "/rapport").Body.String())
	if !strings.Contains(body, "Seuils appliqués") || !strings.Contains(body, "<th scope=\"row\">SNR, 256QAM</th>") ||
		!strings.Contains(body, "Seuils VOO / Orange : valeurs de terrain") {
		t.Error("le rapport ne dit pas sur quels seuils il repose")
	}
}

func TestProtectionsDOffice(t *testing.T) {
	h, _ := serveurAvec(t, 1, essai{Collecting: true})
	rec := get(t, h, "", "/connexion")
	for k, v := range map[string]string{"Cross-Origin-Opener-Policy": "same-origin", "Cross-Origin-Resource-Policy": "same-origin"} {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s : %q", k, got)
		}
	}
	// Un envoi demesure est refuse sans etre lu.
	big := "mot_de_passe=" + strings.Repeat("a", 2<<20)
	req := httptest.NewRequest(http.MethodPost, "/connexion", strings.NewReader(big))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("envoi de 2 Mo : HTTP %d", rec.Code)
	}
}

func TestErreursSNMP(t *testing.T) {
	msg := readError(fmt.Errorf("lecture : %w", modem.ErrNoSNMP), "http://192.168.100.1")
	if !strings.Contains(msg, "en SNMP à 192.168.100.1") || !strings.Contains(msg, "Autre modem") {
		t.Errorf("%s", msg)
	}
	if msg := readError(modem.ErrNoDOCSIS, "http://192.168.100.1"); !strings.Contains(msg, "ne publie pas ses canaux") {
		t.Errorf("%s", msg)
	}
}
