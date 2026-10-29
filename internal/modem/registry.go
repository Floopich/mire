package modem

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/floopich/mire/internal/docsis"
)

// Driver lit un releve brut sur un modem.
//
// C'est tout ce que le reste du programme attend d'un pilote : la collecte,
// le stockage et l'analyse ne connaissent que docsis.RawSnapshot. Ajouter un
// modem revient a ecrire un Driver et a l'inscrire dans models.
type Driver interface {
	Snapshot(ctx context.Context) (docsis.RawSnapshot, error)
}

// Config porte ce qu'il faut pour joindre un modem.
type Config struct {
	URL      string
	User     string
	Password string
}

// Model decrit un modem pris en charge.
type Model struct {
	// ID est l'identifiant court, celui de MIRE_MODELE : minuscules, sans espace.
	ID string
	// Name est le nom commercial, tel qu'il figure sur l'etiquette du modem.
	Name string
	// Firmware precise la variante validee, quand un meme modele existe en
	// plusieurs firmwares d'operateur.
	Firmware string
	// DefaultUser est l'identifiant de connexion le plus courant.
	DefaultUser string
	// Markers sont des fragments, en minuscules, propres a la page d'accueil
	// du modem : ils permettent a « mire detecter » de le reconnaitre.
	Markers []string
	New     func(Config) (Driver, error)
	// Hidden ecarte le modele des listes et de la detection : le modem
	// fictif de la demonstration, que seule une instance de demonstration
	// emploie.
	Hidden bool
}

// models est le registre des pilotes. Un pilote n'y entre qu'avec des
// releves reels dans testdata/ qui couvrent son analyseur.
var models = []Model{
	{
		ID:          "cga4233",
		Name:        "Technicolor CGA4233",
		Firmware:    "firmware VOO",
		DefaultUser: "voo",
		Markers:     []string{"theme/voo", "technicolor"},
		New: func(c Config) (Driver, error) {
			return NewCGA4233(c.URL, c.User, c.Password)
		},
	},
}

// Register inscrit un modele, ou remplace celui du meme identifiant : le
// modem fictif de la demonstration.
func Register(m Model) {
	registry.Lock()
	defer registry.Unlock()
	for i, known := range models {
		if known.ID == m.ID {
			models[i] = m
			return
		}
	}
	models = append(models, m)
}

var registry sync.RWMutex

// Models renvoie les modems pris en charge, tries par identifiant ; les
// modeles caches n'y figurent pas.
func Models() []Model {
	registry.RLock()
	defer registry.RUnlock()
	var out []Model
	for _, m := range models {
		if !m.Hidden {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Lookup renvoie le modele demande.
func Lookup(id string) (Model, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	registry.RLock()
	for _, m := range models {
		if m.ID == id {
			registry.RUnlock()
			return m, nil
		}
	}
	registry.RUnlock()
	var known []string
	for _, m := range Models() {
		known = append(known, m.ID)
	}
	return Model{}, fmt.Errorf("modèle de modem %q inconnu ; modèles pris en charge : %s",
		id, strings.Join(known, ", "))
}

// Label rend le nom complet d'un modele, firmware compris.
func (m Model) Label() string {
	if m.Firmware == "" {
		return m.Name
	}
	return m.Name + " (" + m.Firmware + ")"
}
