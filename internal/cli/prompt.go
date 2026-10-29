// Package cli porte les interactions avec le terminal.
package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// IsTerminal dit si l'entree standard est un terminal.
//
// Sert a ne demander le mot de passe que devant un humain : sous systemd ou
// dans un conteneur, une invite bloquerait le demarrage sans que personne ne
// la voie.
func IsTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// ReadPassword demande un mot de passe sur le terminal.
//
// masked masque la frappe. On le laisse desactivable : un mot de passe long
// se colle, et ne rien voir de ce qu'on a colle rend l'erreur invisible
// jusqu'a l'echec d'authentification.
func ReadPassword(prompt string, masked bool) (string, error) {
	return readPasswordFrom(os.Stdin, os.Stderr, prompt, masked)
}

func readPasswordFrom(in io.Reader, out io.Writer, prompt string, masked bool) (string, error) {
	fmt.Fprint(out, prompt)

	var restore func()
	if masked {
		restore = disableEcho()
		defer func() {
			restore()
			fmt.Fprintln(out)
		}()
	}

	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("lecture du mot de passe: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// ReadLine lit une ligne sur une entree qui n'est pas un terminal.
func ReadLine(in io.Reader) (string, error) {
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("lecture: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}
