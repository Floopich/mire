package settings

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/floopich/mire/internal/store"
)

func base(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestChaqueReglageEstDeclareUneFois(t *testing.T) {
	keys, envs := map[string]bool{}, map[string]bool{}
	for _, g := range Groups() {
		if g.Toggle != "" {
			if d, ok := Lookup(g.Toggle); !ok || d.Kind != Bool {
				t.Errorf("groupe %s : interrupteur %q absent ou non booleen", g.ID, g.Toggle)
			}
		}
		for _, d := range g.Defs {
			if keys[d.Key] {
				t.Errorf("cle %s declaree deux fois", d.Key)
			}
			keys[d.Key] = true
			if d.Env != "" {
				if envs[d.Env] {
					t.Errorf("variable %s declaree deux fois", d.Env)
				}
				envs[d.Env] = true
			}
			if d.Label == "" {
				t.Errorf("%s sans libelle", d.Key)
			}
			if err := validate(d, d.Default); err != nil {
				t.Errorf("%s : la valeur par defaut %q est invalide : %v", d.Key, d.Default, err)
			}
		}
	}
}

func TestPriorite(t *testing.T) {
	db := base(t)
	ctx := context.Background()

	v, err := Load(ctx, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v.Duration(Interval) != 10*time.Minute || v.Locked(Interval) {
		t.Errorf("defaut : %v verrouille=%v", v.Duration(Interval), v.Locked(Interval))
	}

	if err := Set(ctx, db, nil, map[string]string{Interval: "15m"}); err != nil {
		t.Fatal(err)
	}
	v, _ = Load(ctx, db, nil)
	if v.Duration(Interval) != 15*time.Minute {
		t.Errorf("valeur enregistree ignoree : %v", v.Duration(Interval))
	}

	forced := Source{Interval: "5m"}
	v, _ = Load(ctx, db, forced)
	if v.Duration(Interval) != 5*time.Minute || !v.Locked(Interval) {
		t.Errorf("l'environnement doit l'emporter et verrouiller : %v %v", v.Duration(Interval), v.Locked(Interval))
	}
}

func TestUnReglageImposeNeSeModifiePasDepuisLInterface(t *testing.T) {
	db := base(t)
	ctx := context.Background()
	forced := Source{Interval: "5m"}
	if err := Set(ctx, db, forced, map[string]string{Interval: "30m"}); err != nil {
		t.Fatal(err)
	}
	if v, _ := Load(ctx, db, nil); v.Duration(Interval) == 30*time.Minute {
		t.Error("une valeur imposee a ete ecrasee en base")
	}
}

func TestUnMotDePasseVideGardeLAncien(t *testing.T) {
	db := base(t)
	ctx := context.Background()
	if err := Set(ctx, db, nil, map[string]string{ModemPassword: "secret"}); err != nil {
		t.Fatal(err)
	}
	if err := Set(ctx, db, nil, map[string]string{ModemPassword: "", ModemUser: "admin"}); err != nil {
		t.Fatal(err)
	}
	if v, _ := Load(ctx, db, nil); v.String(ModemPassword) != "secret" {
		t.Errorf("mot de passe %q, attendu l'ancien", v.String(ModemPassword))
	}
}

func TestUnChampVideRevientALaValeurParDefaut(t *testing.T) {
	db := base(t)
	ctx := context.Background()
	if err := Set(ctx, db, nil, map[string]string{RetentionDays: ""}); err != nil {
		t.Fatal(err)
	}
	// Vide ne veut pas dire zero : la conservation ne doit pas devenir
	// illimitee parce qu'un champ a ete efface.
	if v, _ := Load(ctx, db, nil); v.Int(RetentionDays) != 3650 {
		t.Errorf("conservation %d, attendu la valeur par defaut", v.Int(RetentionDays))
	}
}

func TestUneSaisieInvalideNEnregistreRien(t *testing.T) {
	db := base(t)
	ctx := context.Background()
	err := Set(ctx, db, nil, map[string]string{ModemUser: "admin", Interval: "10s"})
	if err == nil {
		t.Fatal("un intervalle de 10 s doit etre refuse")
	}
	if v, _ := Load(ctx, db, nil); v.String(ModemUser) != "" {
		t.Error("le groupe a ete enregistre a moitie")
	}
}

func TestValidation(t *testing.T) {
	bad := []Source{
		{Interval: "abc"},
		{Interval: "30s"},
		{RetentionDays: "-3"},
		{RetentionDays: "dix"},
		{RetentionGB: "x"},
		{Profile: "inexistant"},
		{ModemModel: "inconnu"},
		{ModemURL: "192.168.100.1"},
		{OFDMAFloor: "12"},
	}
	for _, s := range bad {
		if err := s.Validate(); err == nil {
			t.Errorf("%v : erreur attendue", s)
		}
	}
	good := Source{Interval: "1h", RetentionDays: "0", RetentionGB: "2,5", Profile: "generique",
		ModemModel: "cga4233", ModemURL: "http://192.168.0.1", OFDMAFloor: "16"}
	if err := good.Validate(); err != nil {
		t.Errorf("valeurs correctes refusees : %v", err)
	}
	if v := (Values{values: map[string]string{RetentionGB: "2,5"}}); v.Float(RetentionGB) != 2.5 {
		t.Errorf("virgule decimale mal lue : %v", v.Float(RetentionGB))
	}
}

func TestLesVariablesDEnvironnementSontLues(t *testing.T) {
	env := map[string]string{"MIRE_MODEM_MOT_DE_PASSE": "secret", "MIRE_INTERVALLE": " 5m ", "AUTRE": "x"}
	s := FromEnv(func(k string) string { return env[k] })
	if s[ModemPassword] != "secret" || s[Interval] != "5m" || len(s) != 2 {
		t.Errorf("source inattendue : %v", s)
	}
}

func TestUneAdresseSansSchemaEstCompletee(t *testing.T) {
	src := FromEnv(func(k string) string {
		return map[string]string{"MIRE_NOTIF_ADRESSE": "mire.exemple.be", "MIRE_MODEM": "192.168.100.1"}[k]
	})
	if src[NotifyLink] != "https://mire.exemple.be" || src[ModemURL] != "http://192.168.100.1" {
		t.Errorf("adresses %v", src)
	}
	if err := src.Validate(); err != nil {
		t.Errorf("une adresse sans schéma ne doit pas empêcher le démarrage : %v", err)
	}
}
