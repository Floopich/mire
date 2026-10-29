//go:build linux || darwin

package cli

import (
	"os"
	"os/exec"
)

// disableEcho coupe l'echo du terminal via stty. Sans stty, la saisie reste
// visible.
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
