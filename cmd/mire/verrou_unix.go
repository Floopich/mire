//go:build unix

package main

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// lockData pose un verrou sur le dossier des donnees, tenu tant que la
// fonction rendue n'est pas appelee. Il vaut entre conteneurs qui partagent
// le volume : une restauration lancee par « docker compose run » voit le
// service en marche, ce qu'une requete sur 127.0.0.1 ne voit pas.
func lockData(dir string) (unlock func(), err error) {
	f, err := os.OpenFile(filepath.Join(dir, ".verrou"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errDataLocked
		}
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
