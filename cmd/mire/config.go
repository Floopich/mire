package main

import (
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/floopich/mire/internal/settings"
)

// config porte ce qu'il faut savoir avant d'ouvrir la base : ou sont les
// donnees, ou ecouter, quoi journaliser. Tout le reste est un reglage
// (internal/settings), modifiable depuis l'interface.
type config struct {
	DataDir string
	Listen  string
	Verbose bool
	// Demo fait tourner Mire sur une ligne fictive, dans une base jetable.
	Demo bool
	// Forced regroupe les reglages imposes par l'environnement ou par les
	// options : ils l'emportent sur la page Reglages et y sont verrouilles.
	Forced settings.Source
}

// flagSettings relie les options historiques de la ligne de commande aux
// reglages qu'elles imposent.
var flagSettings = []struct {
	name, key, usage string
}{
	{"profil", settings.Profile, "profil d'opérateur (MIRE_PROFIL)"},
	{"modem", settings.ModemURL, "adresse du modem (MIRE_MODEM)"},
	{"modele", settings.ModemModel, "modèle du modem ; liste : mire modeles (MIRE_MODELE)"},
	{"utilisateur", settings.ModemUser, "identifiant du modem (MIRE_UTILISATEUR)"},
	{"intervalle", settings.Interval, "délai entre deux relevés, par exemple 10m (MIRE_INTERVALLE)"},
	{"retention", settings.RetentionDays, "conservation en jours, 0 pour illimitée (MIRE_RETENTION)"},
	{"taille-max", settings.RetentionGB, "plafond de la base en Go, 0 pour illimité (MIRE_TAILLE_MAX)"},
}

// parseConfig lit l'environnement puis les options, sans argument libre.
func parseConfig(name string, args []string, getenv func(string) string, out io.Writer) (config, error) {
	c, rest, err := parseConfigArgs(name, args, getenv, out)
	if err == nil && len(rest) > 0 {
		err = fmt.Errorf("argument inattendu : %q", rest[0])
	}
	return c, err
}

// parseConfigArgs lit l'environnement puis les options, et rend les
// arguments qui suivent les options.
func parseConfigArgs(name string, args []string, getenv func(string) string, out io.Writer) (config, []string, error) {
	env := func(key, fallback string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return fallback
	}
	c := config{Forced: settings.FromEnv(getenv)}

	verbose, err := strconv.ParseBool(env("MIRE_VERBEUX", "false"))
	if err != nil {
		return c, nil, fmt.Errorf("MIRE_VERBEUX : valeur attendue true ou false")
	}

	demoMode, err := strconv.ParseBool(env("MIRE_DEMO", "false"))
	if err != nil {
		return c, nil, fmt.Errorf("MIRE_DEMO : valeur attendue true ou false")
	}

	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(out)
	fs.StringVar(&c.DataDir, "donnees", env("MIRE_DONNEES", "."), "dossier des données (MIRE_DONNEES)")
	fs.StringVar(&c.Listen, "ecoute", env("MIRE_ECOUTE", "0.0.0.0:1340"), "adresse d'écoute de l'interface (MIRE_ECOUTE)")
	fs.BoolVar(&c.Verbose, "verbeux", verbose, "journaliser chaque relevé (MIRE_VERBEUX)")
	fs.BoolVar(&c.Demo, "demo", demoMode, "démonstration sur une ligne fictive, sans modem ni données conservées (MIRE_DEMO)")
	overrides := map[string]*string{}
	for _, f := range flagSettings {
		overrides[f.key] = fs.String(f.name, "", f.usage)
	}
	if err := fs.Parse(args); err != nil {
		return c, nil, err
	}
	for key, v := range overrides {
		if s := strings.TrimSpace(*v); s != "" {
			c.Forced[key] = s
		}
	}
	return c, fs.Args(), c.Forced.Validate()
}
