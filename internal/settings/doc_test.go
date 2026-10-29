package settings

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// La documentation suit le registre : chaque variable est decrite dans
// docs/configuration.md, et le
// modele .env.exemple ne propose que des variables reconnues.
func TestDocumentation(t *testing.T) {
	known := map[string]bool{"TZ": true}
	for _, g := range Groups() {
		for _, d := range g.Defs {
			if d.Env != "" {
				known[d.Env] = true
			}
		}
	}

	doc, err := os.ReadFile("../../docs/configuration.md")
	if err != nil {
		t.Fatal(err)
	}
	for env := range known {
		if env != "TZ" && !strings.Contains(string(doc), "`"+env+"`") {
			t.Errorf("docs/configuration.md : %s n'est pas décrite", env)
		}
	}

	exemple, err := os.ReadFile("../../.env.exemple")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile(`(?m)^#?\s*([A-Z][A-Z0-9_]*)=`).FindAllStringSubmatch(string(exemple), -1) {
		if !known[m[1]] {
			t.Errorf(".env.exemple : %s n'est pas une variable de Mire", m[1])
		}
	}
}
