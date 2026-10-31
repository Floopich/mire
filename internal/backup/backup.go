// Package backup garde des copies quotidiennes de la base, sans mots de
// passe ni jetons, dans le dossier des donnees.
package backup

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/floopich/mire/internal/auth"
	"github.com/floopich/mire/internal/settings"
	"github.com/floopich/mire/internal/store"
)

const (
	// Dir est le sous-dossier des donnees qui recoit les copies.
	Dir = "sauvegardes"
	// Every separe deux copies ; une heure de marge evite que la copie
	// glisse d'une heure chaque jour.
	Every = 23 * time.Hour

	prefix     = "mire-"
	suffix     = "-sans-secrets.db"
	nameLayout = "20060102-150405"
	partial    = ".en-cours-"
)

// Les copies attendent quelques minutes apres le demarrage, puis se
// verifient toutes les heures. Modifiables par les tests.
var (
	startDelay = 5 * time.Minute
	checkEvery = time.Hour
)

// SecretKeys sont les cles retirees d'une copie sans secrets : reglages
// secrets et empreinte du mot de passe de l'interface.
func SecretKeys() []string {
	return append(settings.SecretKeys(), auth.KeyPasswordHash)
}

// Copy est une copie automatique.
type Copy struct {
	Path string
	Time time.Time
	Size int64
}

// List renvoie les copies automatiques du dossier des donnees, la plus
// recente d'abord. Les autres fichiers du dossier sont ignores.
func List(dataDir string) ([]Copy, error) {
	entries, err := os.ReadDir(filepath.Join(dataDir, Dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Copy
	for _, e := range entries {
		stamp, ok := strings.CutPrefix(e.Name(), prefix)
		if !ok || e.IsDir() {
			continue
		}
		if stamp, ok = strings.CutSuffix(stamp, suffix); !ok {
			continue
		}
		t, err := time.ParseInLocation(nameLayout, stamp, time.Local)
		if err != nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Copy{Path: filepath.Join(dataDir, Dir, e.Name()), Time: t, Size: info.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out, nil
}

// Keeper ecrit une copie par jour et ne garde que les plus recentes.
type Keeper struct {
	DB      *sql.DB
	DataDir string
	// Keep est le nombre de copies gardees, une au moins.
	Keep   int
	Logger *slog.Logger
	// Now est injectable pour les tests.
	Now func() time.Time
}

func (k *Keeper) now() time.Time {
	if k.Now != nil {
		return k.Now()
	}
	return time.Now()
}

// Run tourne jusqu'a l'annulation du contexte.
func (k *Keeper) Run(ctx context.Context) error {
	k.cleanPartial()
	wait := startDelay
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		if err := k.Once(ctx); err != nil && ctx.Err() == nil {
			k.Logger.Warn("sauvegarde automatique impossible", "erreur", err)
		}
		wait = checkEvery
	}
}

// Once ecrit une copie si la derniere date de plus d'Every, puis retire les
// plus anciennes.
func (k *Keeper) Once(ctx context.Context) error {
	copies, err := List(k.DataDir)
	if err != nil {
		return err
	}
	now := k.now()
	if len(copies) == 0 || now.Sub(copies[0].Time) >= Every {
		dir := filepath.Join(k.DataDir, Dir)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		random := make([]byte, 8)
		_, _ = rand.Read(random)
		tmp := filepath.Join(dir, partial+hex.EncodeToString(random)+".db")
		defer func() { _ = os.Remove(tmp) }()
		if err := store.BackupWithout(ctx, k.DB, tmp, SecretKeys()); err != nil {
			return err
		}
		if err := os.Chmod(tmp, 0o600); err != nil {
			return err
		}
		path := filepath.Join(dir, prefix+now.Local().Format(nameLayout)+suffix)
		if err := os.Rename(tmp, path); err != nil {
			return err
		}
		k.Logger.Info("sauvegarde automatique", "fichier", path)
		if copies, err = List(k.DataDir); err != nil {
			return err
		}
	}
	keep := max(k.Keep, 1)
	for _, c := range copies[min(keep, len(copies)):] {
		if err := os.Remove(c.Path); err != nil {
			return err
		}
	}
	return nil
}

// cleanPartial retire une copie interrompue par un arret de Mire.
func (k *Keeper) cleanPartial() {
	left, _ := filepath.Glob(filepath.Join(k.DataDir, Dir, partial+"*"))
	for _, p := range left {
		_ = os.Remove(p)
	}
}
