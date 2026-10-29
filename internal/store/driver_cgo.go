//go:build !purego

package store

// Pilote SQLite par defaut, adosse a la bibliotheque C.
//
// Il sert au developpement et aux tests. Les binaires distribues sont
// compiles avec -tags purego, qui lui substitue une implementation en Go pur :
// la compilation croisee vers arm64 et armv7 se fait alors sans chaine C, et
// le binaire reste statique.
import _ "github.com/mattn/go-sqlite3"

const driverName = "sqlite3"

// dsn porte les pragmas dans la chaine de connexion.
//
// database/sql tient un pool : un PRAGMA execute une fois ne s'applique qu'a la
// connexion qui l'a recu. Passes ici, ils sont rejoues a chaque nouvelle
// connexion du pool.
func dsn(path string) string {
	return path + "?_journal_mode=WAL&_synchronous=NORMAL&_foreign_keys=on&_busy_timeout=5000"
}
