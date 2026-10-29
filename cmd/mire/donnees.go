package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/floopich/mire/internal/cli"
	"github.com/floopich/mire/internal/store"
)

// checkWritable verifie que le service peut ecrire dans le dossier des
// donnees. Sans ce controle, SQLite echoue plus loin avec un message obscur ;
// c'est le cas d'un dossier monte dans Docker et cree par root.
func checkWritable(dir string) error {
	err := os.MkdirAll(dir, 0o750)
	if err == nil {
		var f *os.File
		if f, err = os.CreateTemp(dir, ".ecriture-*"); err == nil {
			name := f.Name()
			_ = f.Close()
			return os.Remove(name)
		}
	}
	if errors.Is(err, fs.ErrPermission) {
		msg := fmt.Sprintf("le dossier des données %s n'est pas accessible en écriture", dir)
		if uid := os.Getuid(); uid >= 0 {
			msg += fmt.Sprintf(" par l'utilisateur %d : donnez-le lui, par exemple « sudo chown -R %d:%d %s » sur la machine hôte pour un dossier monté dans Docker",
				uid, uid, os.Getgid(), dir)
		}
		return errors.New(msg)
	}
	return err
}

// tempPath renvoie un nom de fichier temporaire dans le dossier des donnees :
// sur le meme systeme de fichiers que la base, il se renomme sans copie.
func tempPath(dir, prefix string) string {
	suffix := make([]byte, 8)
	_, _ = rand.Read(suffix)
	return filepath.Join(dir, "."+prefix+"-"+hex.EncodeToString(suffix)+".db")
}

// backupToStdout ecrit une sauvegarde sur la sortie standard : avec Docker,
// « docker compose exec -T mire mire sauvegarde - > mire.db » la recupere
// sans acceder au volume.
func backupToStdout(ctx context.Context, cfg config, clean bool) error {
	if cli.IsTerminal(os.Stdout) {
		return errors.New("redirigez la sortie vers un fichier : mire sauvegarde - > mire-sauvegarde.db")
	}
	db, err := open(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	tmp := tempPath(cfg.DataDir, "sauvegarde")
	defer func() { _ = os.Remove(tmp) }()
	if err := writeBackup(ctx, db, tmp, clean); err != nil {
		return err
	}
	f, err := os.Open(tmp)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(os.Stdout, f); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Sauvegarde écrite sur la sortie standard.")
	return nil
}

// restore remplace la base par une sauvegarde. Mire doit etre arrete : une
// instance en marche garderait l'ancienne base ouverte. L'ancienne base est
// conservee a cote, sous un nom date.
func restore(args []string) error {
	cfg, rest, err := parseConfigArgs("mire restauration", args, os.Getenv, os.Stderr)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errors.New("indiquez la sauvegarde : mire restauration /chemin/mire-sauvegarde.db, ou - pour l'entrée standard")
	}
	if running(cfg.Listen) {
		return errors.New("Mire tourne encore sur cette machine : arrêtez-le avant de restaurer (docker compose stop, ou systemctl stop mire)")
	}
	if err := checkWritable(cfg.DataDir); err != nil {
		return err
	}

	var src io.Reader
	if rest[0] == "-" {
		if cli.IsTerminal(os.Stdin) {
			return errors.New("envoyez la sauvegarde sur l'entrée standard : mire restauration - < mire-sauvegarde.db")
		}
		src = os.Stdin
	} else {
		f, err := os.Open(rest[0])
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		src = f
	}

	ctx := context.Background()
	tmp := tempPath(cfg.DataDir, "restauration")
	defer func() { _ = os.Remove(tmp) }()
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, src); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := store.Validate(ctx, tmp); err != nil {
		return fmt.Errorf("sauvegarde refusée : %w", err)
	}

	path := filepath.Join(cfg.DataDir, "mire.db")
	kept := ""
	if _, err := os.Stat(path); err == nil {
		kept = path + ".avant-restauration-" + time.Now().Format("20060102-150405")
		// Le journal WAL suit la base sous le meme nom, pour rester utilisable.
		for _, ext := range []string{"", "-wal", "-shm"} {
			if err := os.Rename(path+ext, kept+ext); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if kept != "" {
		fmt.Fprintln(os.Stderr, "Ancienne base conservée :", kept)
	}
	fmt.Fprintln(os.Stderr, "Base restaurée. Relancez Mire : les migrations s'appliqueront au démarrage si la sauvegarde est plus ancienne.")
	return nil
}

// running dit si une instance repond deja sur l'adresse d'ecoute.
func running(listen string) bool {
	url, err := healthURL(listen)
	if err != nil {
		return false
	}
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Get(url)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}
