// Package settings decrit les reglages de Mire et leur valeur effective.
//
// Priorite : variable d'environnement (ou option), qui verrouille le champ ;
// sinon la valeur enregistree ; sinon la valeur par defaut.
package settings

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/floopich/mire/internal/store"
)

// Kind est le type d'un reglage, qui decide de son champ de saisie.
type Kind int

const (
	Text Kind = iota
	Password
	Bool
	Select
	Integer
	Number
	Duration
)

// Option est un choix d'une liste deroulante.
type Option struct {
	Value string
	Label string
}

// Def declare un reglage.
type Def struct {
	Key string
	Env string
	// Normalize complete une saisie courante avant sa verification, par
	// exemple une adresse sans http://.
	Normalize func(string) string
	Kind      Kind
	Default   string
	Label     string
	Help      string
	// Placeholder s'affiche dans un champ vide.
	Placeholder string
	// Options donne les choix d'une liste ; elle est appelee a chaque rendu.
	Options func() []Option
	// Only limite l'affichage du champ a une valeur de la liste de son
	// groupe (par exemple un service de notification) : « notif.service=ntfy ».
	Only string
	// Check valide une valeur avant enregistrement.
	Check func(string) error
}

// Group rassemble des reglages sous un meme titre.
type Group struct {
	ID    string
	Title string
	Help  string
	// Toggle est la cle du reglage booleen qui active le groupe ; ses autres
	// champs ne s'affichent que s'il est coche.
	Toggle string
	// Country limite le groupe aux operateurs d'un pays (code ISO) : la
	// compensation legale belge n'a pas de sens ailleurs.
	Country string
	Defs    []Def
}

// Source fournit les valeurs imposees : variables d'environnement et options
// de la ligne de commande, indexees par cle de reglage.
type Source map[string]string

// FromEnv lit les variables d'environnement des reglages declares.
func FromEnv(getenv func(string) string) Source {
	s := Source{}
	for _, g := range Groups() {
		for _, d := range g.Defs {
			if d.Env == "" {
				continue
			}
			v := strings.TrimSpace(getenv(d.Env))
			if v != "" {
				if d.Normalize != nil {
					v = d.Normalize(v)
				}
				s[d.Key] = v
			}
		}
	}
	return s
}

// Values est l'ensemble des valeurs effectives a un instant donne.
type Values struct {
	values map[string]string
	locked map[string]bool
}

// Load calcule les valeurs effectives.
func Load(ctx context.Context, db *sql.DB, forced Source) (Values, error) {
	v := Values{values: map[string]string{}, locked: map[string]bool{}}
	for _, g := range Groups() {
		for _, d := range g.Defs {
			if f, ok := forced[d.Key]; ok {
				v.values[d.Key] = f
				v.locked[d.Key] = true
				continue
			}
			saved, err := store.Setting(ctx, db, key(d.Key), d.Default)
			if err != nil {
				return v, err
			}
			// Un champ vide dans la page Reglages revient a la valeur par
			// defaut, jamais a zero : une conservation videe ne doit pas
			// devenir illimitee.
			if strings.TrimSpace(saved) == "" {
				saved = d.Default
			}
			v.values[d.Key] = saved
		}
	}
	return v, nil
}

// Fixed construit des valeurs sans base : les valeurs par defaut, completees
// par celles donnees. Il sert aux commandes et aux tests.
func Fixed(given map[string]string) Values {
	v := Values{values: map[string]string{}, locked: map[string]bool{}}
	for _, g := range Groups() {
		for _, d := range g.Defs {
			v.values[d.Key] = d.Default
		}
	}
	for k, value := range given {
		v.values[k] = value
	}
	return v
}

// key prefixe les reglages en base, pour ne pas les confondre avec les
// donnees internes de la table settings (empreinte du mot de passe...).
func key(k string) string { return "reglage." + k }

// String renvoie une valeur texte.
func (v Values) String(k string) string { return v.values[k] }

// Bool renvoie une valeur booleenne, faux si illisible.
func (v Values) Bool(k string) bool {
	b, _ := strconv.ParseBool(v.values[k])
	return b
}

// Int renvoie une valeur entiere, zero si illisible.
func (v Values) Int(k string) int {
	n, _ := strconv.Atoi(v.values[k])
	return n
}

// Float renvoie une valeur decimale, zero si illisible.
func (v Values) Float(k string) float64 {
	f, _ := strconv.ParseFloat(strings.ReplaceAll(v.values[k], ",", "."), 64)
	return f
}

// Duration renvoie une duree, zero si illisible.
func (v Values) Duration(k string) time.Duration {
	d, _ := time.ParseDuration(v.values[k])
	return d
}

// SecretKeys sont les cles en base des reglages secrets : mots de passe,
// jetons, adresses de webhook.
func SecretKeys() []string {
	var out []string
	for _, g := range Groups() {
		for _, d := range g.Defs {
			if d.Kind == Password {
				out = append(out, key(d.Key))
			}
		}
	}
	return out
}

// Locked dit si la valeur est imposee par l'environnement.
func (v Values) Locked(k string) bool { return v.locked[k] }

// Set enregistre des valeurs saisies dans la page Reglages.
//
// Une valeur imposee par l'environnement est ignoree : la page ne peut pas
// contredire la configuration du service. Un mot de passe laisse vide garde
// l'ancien, pour ne pas avoir a le ressaisir a chaque enregistrement.
func Set(ctx context.Context, db *sql.DB, forced Source, input map[string]string) error {
	// Tout est verifie avant d'ecrire : une erreur ne laisse jamais un groupe
	// a moitie enregistre.
	accepted := map[string]string{}
	for k, raw := range input {
		d, ok := Lookup(k)
		if !ok {
			return fmt.Errorf("réglage inconnu : %s", k)
		}
		if _, isForced := forced[k]; isForced {
			continue
		}
		value := strings.TrimSpace(raw)
		if d.Kind == Password && value == "" {
			continue
		}
		if d.Normalize != nil && value != "" {
			value = d.Normalize(value)
		}
		if err := validate(d, value); err != nil {
			return fmt.Errorf("%s : %w", d.Label, err)
		}
		accepted[k] = value
	}
	for k, value := range accepted {
		if err := store.SetSetting(ctx, db, key(k), value); err != nil {
			return err
		}
	}
	return nil
}

// Validate verifie une valeur imposee par l'environnement, pour refuser un
// demarrage mal configure plutot que de l'ignorer en silence.
func (s Source) Validate() error {
	for k, value := range s {
		d, ok := Lookup(k)
		if !ok {
			continue
		}
		if err := validate(d, value); err != nil {
			if d.Env != "" {
				return fmt.Errorf("%s : %w", d.Env, err)
			}
			return fmt.Errorf("%s : %w", d.Label, err)
		}
	}
	return nil
}

func validate(d Def, value string) error {
	switch d.Kind {
	case Bool:
		if value == "" {
			return nil
		}
		if _, err := strconv.ParseBool(value); err != nil {
			return fmt.Errorf("valeur attendue : vrai ou faux")
		}
	case Integer:
		if value == "" {
			return nil
		}
		if _, err := strconv.Atoi(value); err != nil {
			return fmt.Errorf("nombre entier attendu")
		}
	case Number:
		if value == "" {
			return nil
		}
		if _, err := strconv.ParseFloat(strings.ReplaceAll(value, ",", "."), 64); err != nil {
			return fmt.Errorf("nombre attendu")
		}
	case Duration:
		if value == "" {
			return nil
		}
		if _, err := time.ParseDuration(value); err != nil {
			return fmt.Errorf("durée attendue, par exemple 10m ou 1h")
		}
	case Select:
		if value == "" || d.Options == nil {
			break
		}
		found := false
		for _, o := range d.Options() {
			if o.Value == value {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("choix inconnu : %q", value)
		}
	}
	if d.Check != nil {
		return d.Check(value)
	}
	return nil
}

// Lookup renvoie la declaration d'un reglage.
func Lookup(k string) (Def, bool) {
	for _, g := range Groups() {
		for _, d := range g.Defs {
			if d.Key == k {
				return d, true
			}
		}
	}
	return Def{}, false
}

// GroupByID renvoie un groupe.
func GroupByID(id string) (Group, bool) {
	for _, g := range Groups() {
		if g.ID == id {
			return g, true
		}
	}
	return Group{}, false
}
