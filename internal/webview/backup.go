package webview

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/floopich/mire/internal/store"
)

// handleBackup envoie une copie coherente de la base, prise sans arreter la
// collecte. La copie est ecrite dans le dossier des donnees, envoyee, puis
// effacee : une base de plusieurs gigaoctets ne tient pas en memoire.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	suffix := make([]byte, 8)
	_, _ = rand.Read(suffix)
	tmp := filepath.Join(s.opts.DataDir, ".sauvegarde-"+hex.EncodeToString(suffix)+".db")
	defer func() { _ = os.Remove(tmp) }()

	if err := store.Backup(r.Context(), s.db, tmp); err != nil {
		s.fail(w, err)
		return
	}
	f, err := os.Open(tmp)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		s.fail(w, err)
		return
	}

	// Le delai d'ecriture du serveur vaut pour une page, pas pour une base
	// de plusieurs gigaoctets sur une connexion lente.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	name := "mire-" + s.opts.Now().Format("20060102-1504") + ".db"
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("Cache-Control", "no-store")
	if _, err := io.Copy(w, f); err != nil {
		s.opts.Logger.Warn("sauvegarde interrompue", "erreur", err)
	}
}
