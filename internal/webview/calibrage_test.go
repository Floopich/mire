package webview

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/settings"
	"github.com/floopich/mire/internal/store"
)

func TestArchiveDeCalibrage(t *testing.T) {
	db := baseRemplie(t, 5)
	ctx := context.Background()
	if err := store.AddNote(ctx, db, time.Now().Add(-time.Minute), "note-temoin"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveSpeedTests(ctx, db, []store.SpeedTest{{Time: time.Now().Add(-2 * time.Minute), Download: 900, Upload: 40, Server: "serveur-temoin"}}); err != nil {
		t.Fatal(err)
	}
	forced := settings.Source{settings.ModemModel: "cga4233", settings.ModemPassword: "secret-temoin",
		settings.ModemURL: "http://192.0.2.77", settings.Profile: "voo"}
	h, _ := monter(t, db, essai{Collecting: true, Forced: forced, Version: "v9.9.9"})
	token := session(t, db)

	page := get(t, h, token, "/rapport").Body.String()
	if !strings.Contains(page, "/rapport/calibrage.zip?") || !strings.Contains(page, "template=calibrage.yml") {
		t.Fatal("le rapport doit proposer l'archive de calibrage")
	}
	rec := get(t, h, token, "/rapport/calibrage.zip")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("HTTP %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	contenu := map[string]string{}
	var noms []string
	for _, f := range zr.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r)
		_ = r.Close()
		contenu[f.Name] = string(b)
		noms = append(noms, f.Name)
	}
	sort.Strings(noms)
	if strings.Join(noms, " ") != "coupures.csv descendant.csv journal.csv ligne.json lisez-moi.txt montant.csv" {
		t.Errorf("fichiers : %v", noms)
	}
	for nom, texte := range contenu {
		for _, temoin := range []string{"note-temoin", "serveur-temoin", "secret-temoin", "192.0.2.77"} {
			if strings.Contains(texte, temoin) {
				t.Errorf("%s contient %q", nom, temoin)
			}
		}
	}
	var info calibrationInfo
	if err := json.Unmarshal([]byte(contenu["ligne.json"]), &info); err != nil || info.Mire != "v9.9.9" || info.Profil != "voo" || info.Modem != "cga4233" {
		t.Errorf("ligne.json : %+v %v", info, err)
	}
	if n := strings.Count(contenu["descendant.csv"], "\n"); n < 6 {
		t.Errorf("descendant.csv : %d lignes", n)
	}
}
