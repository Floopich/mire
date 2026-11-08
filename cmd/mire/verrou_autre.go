//go:build !unix

package main

// lockData ne verrouille rien hors Unix : la restauration s'appuie alors sur
// la seule verification de l'adresse d'ecoute.
func lockData(string) (func(), error) { return func() {}, nil }
