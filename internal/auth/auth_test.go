package auth

import (
	"context"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/floopich/mire/internal/store"
)

func TestEmpreinteVerifieLeBonMotDePasse(t *testing.T) {
	h, err := HashPassword("correct-cheval-pile")
	if err != nil {
		t.Fatalf("hachage: %v", err)
	}
	ok, err := VerifyPassword("correct-cheval-pile", h)
	if err != nil || !ok {
		t.Errorf("le bon mot de passe est refuse: %v %v", ok, err)
	}
	ok, _ = VerifyPassword("mauvais", h)
	if ok {
		t.Error("un mauvais mot de passe est accepte")
	}
}

func TestEmpreinteNeContientPasLeMotDePasse(t *testing.T) {
	h, _ := HashPassword("secret-tres-reconnaissable")
	if strings.Contains(h, "secret-tres-reconnaissable") {
		t.Error("le mot de passe apparait en clair dans l'empreinte")
	}
	if !strings.HasPrefix(h, "pbkdf2-sha256$") {
		t.Errorf("format inattendu: %s", h)
	}
}

func TestDeuxEmpreintesDuMemeMotDePasseDifferent(t *testing.T) {
	// Sans sel aleatoire, deux comptes avec le meme mot de passe auraient la
	// meme empreinte, ce qui se voit dans une base volee.
	a, _ := HashPassword("identique")
	b, _ := HashPassword("identique")
	if a == b {
		t.Error("le sel n'est pas aleatoire")
	}
}

func TestEmpreinteCorrompueEstRefusee(t *testing.T) {
	for _, mauvaise := range []string{"", "n'importe quoi", "pbkdf2-sha256$abc$x$y",
		"autre-schema$600000$aaaa$bbbb"} {
		if _, err := VerifyPassword("x", mauvaise); err == nil {
			t.Errorf("%q devrait etre refusee", mauvaise)
		}
	}
}

func TestSessionCreeePuisValide(t *testing.T) {
	s := sessionsTest(t)
	ctx := context.Background()
	token, err := s.Create(ctx)
	if err != nil {
		t.Fatalf("creation: %v", err)
	}
	if err := s.Valid(ctx, token); err != nil {
		t.Errorf("session refusee: %v", err)
	}
	if err := s.Valid(ctx, "jeton-invente"); err == nil {
		t.Error("un jeton invente est accepte")
	}
	if err := s.Valid(ctx, ""); err == nil {
		t.Error("un jeton vide est accepte")
	}
}

func TestLeJetonNEstPasStockeEnClair(t *testing.T) {
	// Une copie de la base ne doit pas suffire a se connecter a la place de
	// quelqu'un.
	db, err := store.Open(filepath.Join(t.TempDir(), "mire.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	s := NewSessions(db)
	token, _ := s.Create(context.Background())

	var stocke string
	if err := db.QueryRow(`SELECT token_hash FROM sessions`).Scan(&stocke); err != nil {
		t.Fatal(err)
	}
	if stocke == token {
		t.Error("le jeton est stocke tel quel")
	}
}

func TestSessionExpireeEstRefuseePuisPurgee(t *testing.T) {
	s := sessionsTest(t)
	ctx := context.Background()
	token, _ := s.Create(ctx)

	s.now = func() time.Time { return time.Now().Add(SessionLifetime + time.Hour) }
	if err := s.Valid(ctx, token); err == nil {
		t.Error("une session expiree est acceptee")
	}
	if err := s.Prune(ctx); err != nil {
		t.Fatalf("purge: %v", err)
	}
}

func TestRevocationGlobale(t *testing.T) {
	// Apres un changement de mot de passe, une session ouverte par quelqu'un
	// qui connaissait l'ancien ne doit pas survivre trente jours.
	s := sessionsTest(t)
	ctx := context.Background()
	a, _ := s.Create(ctx)
	b, _ := s.Create(ctx)
	if err := s.RevokeAll(ctx); err != nil {
		t.Fatal(err)
	}
	if s.Valid(ctx, a) == nil || s.Valid(ctx, b) == nil {
		t.Error("une session survit a la revocation globale")
	}
}

func TestLimiteurRalentitApresTroisEchecs(t *testing.T) {
	l := NewLimiter()
	if l.Wait("1.2.3.4") != 0 {
		t.Error("une adresse inconnue ne doit pas attendre")
	}
	// Les deux premieres erreurs sont des fautes de frappe.
	l.Fail("1.2.3.4")
	l.Fail("1.2.3.4")
	if l.Wait("1.2.3.4") != 0 {
		t.Error("deux echecs ne devraient pas bloquer")
	}
	l.Fail("1.2.3.4")
	if l.Wait("1.2.3.4") <= 0 {
		t.Error("le troisieme echec devrait imposer un delai")
	}
	// Le delai double.
	premier := l.Wait("1.2.3.4")
	l.Fail("1.2.3.4")
	if l.Wait("1.2.3.4") <= premier {
		t.Error("le delai ne croit pas")
	}
	l.Succeed("1.2.3.4")
	if l.Wait("1.2.3.4") != 0 {
		t.Error("une connexion reussie doit effacer l'historique")
	}
}

func TestLimiteurIsoleLesAdresses(t *testing.T) {
	l := NewLimiter()
	for range 5 {
		l.Fail("10.0.0.1")
	}
	if l.Wait("10.0.0.2") != 0 {
		t.Error("une adresse innocente est bloquee par une autre")
	}
}

func TestUneAncienneEmpreinteResteVerifiable(t *testing.T) {
	// Le nombre d'iterations est inscrit dans l'empreinte : relever la valeur
	// par defaut ne doit jamais enfermer quelqu'un dehors. On verifie avec un
	// compte volontairement different de celui du code.
	salt := base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef"))
	key, err := pbkdf2.Key(sha256.New, "mot-de-passe", []byte("0123456789abcdef"), 1000, 32)
	if err != nil {
		t.Fatal(err)
	}
	ancienne := "pbkdf2-sha256$1000$" + salt + "$" +
		base64.RawStdEncoding.EncodeToString(key)

	ok, err := VerifyPassword("mot-de-passe", ancienne)
	if err != nil || !ok {
		t.Errorf("une empreinte a 1000 iterations doit rester verifiable: %v %v", ok, err)
	}

	neuve, _ := HashPassword("mot-de-passe")
	if !strings.Contains(neuve, "$200000$") {
		t.Errorf("la nouvelle empreinte devrait porter 200000 : %s", neuve)
	}
}

func sessionsTest(t *testing.T) *Sessions {
	t.Helper()
	return NewSessions(base(t))
}
