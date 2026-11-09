package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

// Arreter Mire pendant que la demonstration s'ecrit (SIGTERM au demarrage
// d'un conteneur) rend une erreur, sans plantage.
func TestDemoInterrompueAuDemarrage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d, err := startDemo(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), false)
	if err == nil {
		d.Close()
		t.Fatal("démonstration démarrée malgré l'arrêt demandé")
	}
}
