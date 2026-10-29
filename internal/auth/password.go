// Package auth porte le mot de passe administrateur et les sessions.
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// iterations reste supportable sur un Raspberry Pi. Le compte est inscrit
// dans chaque empreinte : il peut augmenter sans invalider les mots de passe
// enregistres.
const iterations = 200_000

const (
	saltLength = 16
	keyLength  = 32
	scheme     = "pbkdf2-sha256"
)

// HashPassword derive l'empreinte d'un mot de passe.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, iterations, keyLength)
	if err != nil {
		return "", err
	}
	enc := base64.RawStdEncoding
	return fmt.Sprintf("%s$%d$%s$%s", scheme, iterations,
		enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// VerifyPassword compare un mot de passe a son empreinte en temps constant.
func VerifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != scheme {
		return false, errors.New("empreinte au format inconnu")
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 {
		return false, errors.New("empreinte: nombre d'iterations illisible")
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[2])
	if err != nil {
		return false, errors.New("empreinte: sel illisible")
	}
	want, err := enc.DecodeString(parts[3])
	if err != nil {
		return false, errors.New("empreinte: cle illisible")
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
