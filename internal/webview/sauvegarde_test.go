package webview

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/floopich/mire/internal/backup"
)

func TestRubriqueSauvegarde(t *testing.T) {
	db := baseRemplie(t, 1)
	dir := t.TempDir()
	h, _ := monter(t, db, essai{DataDir: dir})
	token := session(t, db)

	body := get(t, h, token, "/reglages/sauvegarde").Body.String()
	for _, attendu := range []string{"Chaque jour, 7 copies gardées", "Aucune copie automatique", `name="sauvegarde.copies"`,
		"/reglages/sauvegarde/telecharger?secrets=non"} {
		if !strings.Contains(body, attendu) {
			t.Errorf("%q absent", attendu)
		}
	}
	k := &backup.Keeper{DB: db, DataDir: dir, Keep: 7, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := k.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if body = get(t, h, token, "/reglages/sauvegarde").Body.String(); !strings.Contains(body, "Dernière copie") {
		t.Error("la dernière copie doit être indiquée")
	}
}
