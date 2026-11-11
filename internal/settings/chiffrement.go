package settings

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/floopich/mire/internal/store"
)

// Chiffrement des secrets enregistres, facultatif.
//
// Sans cle, rien ne change : la base est lisible par le seul compte du
// service. Avec une cle donnee hors du dossier des donnees (variable, ou
// fichier comme un secret Docker), les mots de passe et jetons enregistres
// sont chiffres : une copie de la base ou une sauvegarde complete ne les
// livre plus sans la cle.

// KeyEnv donne la cle de chiffrement des secrets ; KeyEnv+FileSuffix la lit
// dans un fichier.
const KeyEnv = "MIRE_CLE_SECRETS"

// KeyCheck est la ligne de controle : une valeur connue, chiffree avec la
// cle. Elle dit que la base est chiffree et refuse une cle differente.
const KeyCheck = "secrets.controle"

const (
	sealPrefix = "mire-chiffre-v1:"
	checkText  = "mire"
)

// ErrKeyFormat decrit la cle attendue.
var ErrKeyFormat = errors.New("64 caractères hexadécimaux attendus, par exemple : openssl rand -hex 32")

// Key est une cle AES-256.
type Key [32]byte

// ParseKey lit une cle de 32 octets en hexadecimal (openssl rand -hex 32)
// ou en base64 (openssl rand -base64 32). Une phrase de passe est refusee :
// trop faible sans derivation couteuse, qui ralentirait chaque commande.
func ParseKey(s string) (*Key, error) {
	s = strings.TrimSpace(s)
	var k Key
	if b, err := hex.DecodeString(s); err == nil && len(b) == len(k) {
		copy(k[:], b)
		return &k, nil
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == len(k) {
		copy(k[:], b)
		return &k, nil
	}
	return nil, ErrKeyFormat
}

// KeyFrom lit la cle dans l'environnement : variable directe ou fichier.
// Sans l'une ni l'autre, elle rend nil : les secrets restent en clair.
func KeyFrom(getenv func(string) string, read func(string) ([]byte, error)) (*Key, error) {
	direct := strings.TrimSpace(getenv(KeyEnv))
	path := strings.TrimSpace(getenv(KeyEnv + FileSuffix))
	switch {
	case direct != "" && path != "":
		return nil, fmt.Errorf("%s et %s : choisissez l'un ou l'autre", KeyEnv, KeyEnv+FileSuffix)
	case direct != "":
		k, err := ParseKey(direct)
		if err != nil {
			return nil, fmt.Errorf("%s : %w", KeyEnv, err)
		}
		return k, nil
	case path != "":
		b, err := read(path)
		if err != nil {
			return nil, fmt.Errorf("%s : fichier %s illisible", KeyEnv+FileSuffix, path)
		}
		if len(b) > maxSecretFile {
			return nil, fmt.Errorf("%s : fichier %s trop gros pour une clé", KeyEnv+FileSuffix, path)
		}
		k, err := ParseKey(string(b))
		if err != nil {
			return nil, fmt.Errorf("%s : %w", KeyEnv+FileSuffix, err)
		}
		return k, nil
	}
	return nil, nil
}

// sealer garde l'AEAD de la cle en service.
type sealer struct{ aead cipher.AEAD }

var active atomic.Pointer[sealer]

// UseKey installe la cle des secrets, ou la retire avec nil. Elle est donnee
// une fois au demarrage, avant d'ouvrir la base.
func UseKey(k *Key) {
	if k == nil {
		active.Store(nil)
		return
	}
	block, err := aes.NewCipher(k[:])
	if err != nil {
		panic(err) // une cle de 32 octets est toujours valable
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	active.Store(&sealer{aead: aead})
}

// KeyInUse dit si une cle de chiffrement des secrets est donnee.
func KeyInUse() bool { return active.Load() != nil }

func sealed(v string) bool { return strings.HasPrefix(v, sealPrefix) }

// seal chiffre une valeur. La cle de la ligne entre dans le calcul : une
// valeur recopiee sur une autre ligne ne se dechiffre pas.
func (s *sealer) seal(dbKey, v string) string {
	nonce := make([]byte, s.aead.NonceSize())
	_, _ = rand.Read(nonce)
	out := s.aead.Seal(nonce, nonce, []byte(v), []byte(dbKey))
	return sealPrefix + base64.RawStdEncoding.EncodeToString(out)
}

func (s *sealer) open(dbKey, v string) (string, error) {
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(v, sealPrefix))
	n := s.aead.NonceSize()
	if err != nil || len(raw) < n {
		return "", fmt.Errorf("secret chiffré illisible : %s", dbKey)
	}
	plain, err := s.aead.Open(nil, raw[:n], raw[n:], []byte(dbKey))
	if err != nil {
		return "", fmt.Errorf("secret indéchiffrable avec cette clé : %s", dbKey)
	}
	return string(plain), nil
}

// isSecretKey dit si une cle en base est celle d'un reglage secret.
func isSecretKey(dbKey string) bool {
	for _, k := range SecretKeys() {
		if k == dbKey {
			return true
		}
	}
	return false
}

// encode prepare une valeur avant de l'enregistrer.
func encode(dbKey, v string) string {
	if s := active.Load(); s != nil && v != "" && isSecretKey(dbKey) {
		return s.seal(dbKey, v)
	}
	return v
}

// decode rend la valeur d'une ligne lue en base.
func decode(dbKey, v string) (string, error) {
	if !sealed(v) {
		return v, nil
	}
	s := active.Load()
	if s == nil {
		return "", errNoKey
	}
	return s.open(dbKey, v)
}

var errNoKey = fmt.Errorf("les secrets de cette base sont chiffrés : donnez la clé avec %s ou %s", KeyEnv, KeyEnv+FileSuffix)

// CheckSecrets verifie, sans rien ecrire, que la cle donnee convient a la
// base : meme cle que celle qui a chiffre, et pas de secret chiffre sans
// cle. Mieux vaut refuser de demarrer qu'envoyer un mot de passe faux au
// modem.
func CheckSecrets(ctx context.Context, db *sql.DB) error {
	check, err := store.Setting(ctx, db, KeyCheck, "")
	if err != nil {
		return err
	}
	s := active.Load()
	if s == nil {
		if check != "" {
			return errNoKey
		}
		n, err := countSealed(ctx, db)
		if err != nil {
			return err
		}
		if n > 0 {
			return errNoKey
		}
		return nil
	}
	if check == "" {
		return nil // pas encore chiffree : SealSecrets s'en charge
	}
	if v, err := s.open(KeyCheck, check); err != nil || v != checkText {
		return fmt.Errorf("%s ne correspond pas à la clé qui a chiffré les secrets de cette base", KeyEnv)
	}
	return nil
}

func countSealed(ctx context.Context, db *sql.DB) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM settings WHERE substr(value, 1, ?) = ?`,
		len(sealPrefix), sealPrefix).Scan(&n)
	return n, err
}

// SealSecrets chiffre les secrets encore en clair et pose la ligne de
// controle, en une transaction. Sans cle, il ne fait rien. Il rend le
// nombre de secrets chiffres.
func SealSecrets(ctx context.Context, db *sql.DB) (int, error) {
	s := active.Load()
	if s == nil {
		return 0, nil
	}
	if err := CheckSecrets(ctx, db); err != nil {
		return 0, err
	}
	return rewrite(ctx, db, func(dbKey, v string) (string, bool, error) {
		if sealed(v) || v == "" {
			return v, false, nil
		}
		return s.seal(dbKey, v), true, nil
	}, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO settings(key, value) VALUES (?, ?) ON CONFLICT(key) DO NOTHING`,
			KeyCheck, s.seal(KeyCheck, checkText))
		return err
	})
}

// purge vide le journal WAL apres un passage au chiffrement : il garde sinon
// une copie des pages ou les secrets etaient en clair.
func purge(ctx context.Context, db *sql.DB) {
	_, _ = db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
}

// UnsealSecrets remet les secrets en clair avec la cle donnee et retire la
// ligne de controle : on peut ensuite demarrer sans cle, ou avec une autre.
func UnsealSecrets(ctx context.Context, db *sql.DB) (int, error) {
	s := active.Load()
	if s == nil {
		return 0, fmt.Errorf("donnez la clé actuelle avec %s ou %s", KeyEnv, KeyEnv+FileSuffix)
	}
	if err := CheckSecrets(ctx, db); err != nil {
		return 0, err
	}
	return rewrite(ctx, db, func(dbKey, v string) (string, bool, error) {
		if !sealed(v) {
			return v, false, nil
		}
		p, err := s.open(dbKey, v)
		return p, err == nil, err
	}, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, KeyCheck)
		return err
	})
}

// rewrite applique f a chaque secret enregistre, puis finish, dans une
// seule transaction : une erreur laisse la base telle qu'elle etait.
func rewrite(ctx context.Context, db *sql.DB, f func(dbKey, v string) (string, bool, error), finish func(*sql.Tx) error) (int, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	n := 0
	for _, k := range SecretKeys() {
		var v string
		err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, k).Scan(&v)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return 0, err
		}
		out, changed, err := f(k, v)
		if err != nil {
			return 0, err
		}
		if !changed {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE settings SET value = ? WHERE key = ?`, out, k); err != nil {
			return 0, err
		}
		n++
	}
	if err := finish(tx); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if n > 0 {
		purge(ctx, db)
	}
	return n, nil
}

// Sealed dit si la base garde ses secrets chiffres.
func Sealed(ctx context.Context, db *sql.DB) bool {
	v, err := store.Setting(ctx, db, KeyCheck, "")
	return err == nil && v != ""
}
