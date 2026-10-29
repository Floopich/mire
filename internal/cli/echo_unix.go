//go:build linux || darwin

package cli

import (
	"os"
	"os/exec"
)

// disableEcho coupe l'echo du terminal via stty.
//
// Passer par stty plutot que par un appel termios evite une dependance et du
// code specifique a chaque systeme, pour une invite qui sert une fois a
// l'installation. Si stty manque, la saisie reste visible : mieux vaut un
// mot de passe affiche qu'un demarrage impossible.
func disableEcho() func() {
	if err := stty("-echo"); err != nil {
		return func() {}
	}
	return func() { _ = stty("echo") }
}

func stty(arg string) error {
	cmd := exec.Command("stty", arg)
	cmd.Stdin = os.Stdin
	return cmd.Run()
}
