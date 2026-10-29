//go:build !linux && !darwin

package cli

// disableEcho ne fait rien sur les systemes ou stty n'est pas disponible.
func disableEcho() func() { return func() {} }
