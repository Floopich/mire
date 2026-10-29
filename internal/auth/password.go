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

// iterations est calibre pour un Raspberry Pi 3B+, la cible du projet.
//
// L'OWASP recommande 600 000 pour PBKDF2-HMAC-SHA256, mais cette valeur vise
// des processeurs de serveur : sur un Pi 3B+ elle impose trois secondes et
// demie a chaque connexion. Le mot de passe genere porte environ 92 bits
// d'entropie, qu'aucun compte d'iterations ne rend attaquable ; les
// iterations ne protegent reellement qu'un mot de passe faible choisi a la
// main, et le limiteur de tentatives couvre deja ce cas.
//
// Le compte est inscrit dans chaque empreinte : on pourra le relever plus
// tard sans invalider les mots de passe deja enregistres.
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
//
// Une comparaison ordinaire s'arreterait au premier octet different, et le
// temps de reponse revelerait combien d'octets sont justes.
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
