//go:build purego

package store

// Pilote SQLite en Go pur, utilise pour les binaires distribues.
//
// Meme interface database/sql que la variante cgo, seul le nom enregistre
// change. Compiler avec -tags purego.
import _ "modernc.org/sqlite"

const driverName = "sqlite"

// dsn porte les pragmas dans la chaine de connexion, pour qu'ils
// s'appliquent a chaque connexion du pool et pas seulement a la premiere.
func dsn(path string) string {
	return path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=secure_delete(1)"
}
