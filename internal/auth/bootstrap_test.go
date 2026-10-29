package auth

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/floopich/mire/internal/store"
)

func base(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatalf("ouverture: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSansMotDePasseLInstanceLeSignale(t *testing.T) {
	db := base(t)
	ctx := context.Background()
	if ok, err := HasPassword(ctx, db); err != nil || ok {
		t.Fatalf("une base neuve n'a pas de mot de passe : %v %v", ok, err)
	}
	if ok, _ := Check(ctx, db, ""); ok {
		t.Error("sans mot de passe, aucune connexion ne doit passer")
	}
}

func TestChoixDuMotDePasse(t *testing.T) {
	db := base(t)
	ctx := context.Background()
	if err := SetPassword(ctx, db, "court"); err == nil {
		t.Error("un mot de passe trop court doit etre refuse")
	}
	if err := SetPassword(ctx, db, "un-mot-de-passe-solide"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := HasPassword(ctx, db); !ok {
		t.Error("le mot de passe n'est pas enregistre")
	}
	if ok, _ := Check(ctx, db, "un-mot-de-passe-solide"); !ok {
		t.Error("le bon mot de passe est refuse")
	}
	if ok, _ := Check(ctx, db, "autre-chose"); ok {
		t.Error("un mauvais mot de passe est accepte")
	}
}

func TestChangerLeMotDePasseFermeLesSessions(t *testing.T) {
	db := base(t)
	ctx := context.Background()
	_ = SetPassword(ctx, db, "premier-mot-de-passe")
	token, err := NewSessions(db).Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := SetPassword(ctx, db, "second-mot-de-passe"); err != nil {
		t.Fatal(err)
	}
	if err := NewSessions(db).Valid(ctx, token); err == nil {
		t.Error("une session ouverte avant le changement reste valide")
	}
}

func TestLePremierMotDePasseNeSeChoisitQuUneFois(t *testing.T) {
	db := base(t)
	ctx := context.Background()
	if err := SetInitialPassword(ctx, db, "premier-choix"); err != nil {
		t.Fatal(err)
	}
	if err := SetInitialPassword(ctx, db, "second-choix"); !errors.Is(err, ErrPasswordExists) {
		t.Fatalf("second choix : %v, attendu ErrPasswordExists", err)
	}
	if ok, _ := Check(ctx, db, "premier-choix"); !ok {
		t.Error("le premier mot de passe a ete remplace")
	}
}
