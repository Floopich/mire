package webview

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/floopich/mire/internal/auth"
)

const sessionCookie = "mire_session"

// requireAuth protege une route.
//
// Tout est ferme sauf la connexion et la supervision : les releves disent
// quand la maison est vide, et l'instance tourne souvent sur un reseau partage.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// La demonstration s'ouvre sans connexion : rien n'y est reel.
		if !s.opts.Demo && !s.loggedIn(r) {
			// Sans mot de passe, il n'y a encore personne a connecter :
			// l'assistant de premier demarrage commence par le choisir.
			target := "/connexion"
			if ok, err := auth.HasPassword(r.Context(), s.db); err == nil && !ok {
				target = "/installation"
			}
			http.Redirect(w, r, target, http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func (s *Server) loggedIn(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookie)
	return err == nil && s.sessions.Valid(r.Context(), cookie.Value) == nil
}

type loginView struct {
	common
	Err string
}

func (s *Server) renderLogin(w http.ResponseWriter, status int, msg string) {
	s.render(w, status, "login", loginView{common: s.bareCommon("Connexion"), Err: msg})
}

func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if s.opts.Demo {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if ok, err := auth.HasPassword(r.Context(), s.db); err == nil && !ok {
		http.Redirect(w, r, "/installation", http.StatusSeeOther)
		return
	}
	// Deja connecte : inutile de redemander.
	if cookie, err := r.Cookie(sessionCookie); err == nil &&
		s.sessions.Valid(r.Context(), cookie.Value) == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.renderLogin(w, http.StatusOK, "")
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	key := clientKey(r)

	// Le SameSite=Strict du cookie bloque deja la soumission depuis un autre
	// site ; on refuse en plus une origine etrangere, qui n'a aucune raison
	// d'exister sur un formulaire local.
	if !sameOrigin(r) {
		http.Error(w, "origine refusée", http.StatusForbidden)
		return
	}
	if wait := s.limiter.Wait(key); wait > 0 {
		s.renderLogin(w, http.StatusTooManyRequests,
			"Trop de tentatives. Réessayez dans "+humanDelay(wait)+".")
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "formulaire illisible", http.StatusBadRequest)
		return
	}

	ok, err := auth.Check(ctx, s.db, r.PostFormValue("mot_de_passe"))
	if err != nil || !ok {
		s.limiter.Fail(key)
		// Le message ne distingue pas "pas de mot de passe configure" de
		// "mauvais mot de passe" : la difference renseignerait un curieux.
		s.renderLogin(w, http.StatusUnauthorized, "Mot de passe incorrect.")
		return
	}

	s.limiter.Succeed(key)
	if err := s.openSession(w, r); err != nil {
		http.Error(w, "session impossible", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// openSession cree une session et pose son cookie.
func (s *Server) openSession(w http.ResponseWriter, r *http.Request) error {
	token, err := s.sessions.Create(r.Context())
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   isHTTPS(r),
		Expires:  time.Now().Add(auth.SessionLifetime),
	})
	return nil
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "origine refusée", http.StatusForbidden)
		return
	}
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		_ = s.sessions.Revoke(r.Context(), cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1,
	})
	http.Redirect(w, r, "/connexion", http.StatusSeeOther)
}

// isHTTPS dit si le navigateur parle en HTTPS, directement ou derriere un
// proxy inverse qui termine le TLS.
//
// L'en-tete n'est lu que pour poser l'attribut Secure du cookie : le
// falsifier ne donne aucun acces, au pire un cookie que le navigateur
// n'enverra pas en clair.
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// clientKey identifie l'appelant pour le limiteur.
//
// On prend l'adresse de la connexion, pas un en-tete : X-Forwarded-For se
// falsifie, et une adresse falsifiee permettrait de contourner le limiteur en
// changeant d'identite a chaque essai. Derriere un proxy inverse, tous les
// appelants partagent donc le meme compteur : c'est plus strict, pas moins.
func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// sameOrigin verifie qu'une requete d'ecriture vient de cette instance.
//
// Sec-Fetch-Site, envoye par tous les navigateurs actuels, tranche seul.
// A defaut, l'hote de l'en-tete Origin (ou du Referer) doit etre exactement
// celui de la requete : une comparaison par inclusion laisserait passer
// mire.exemple.be.attaquant.net.
func sameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "cross-site", "same-site":
		return false
	}
	for _, h := range []string{"Origin", "Referer"} {
		raw := r.Header.Get(h)
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil {
			return false
		}
		return u.Host == r.Host
	}
	// Ni l'un ni l'autre : client non navigateur, que le cookie SameSite ne
	// concerne pas.
	return true
}

func humanDelay(d time.Duration) string {
	if d < time.Minute {
		return strconv.Itoa(int(d.Round(time.Second).Seconds())) + nbsp + "s"
	}
	return strconv.Itoa(int(d.Round(time.Minute).Minutes())) + nbsp + "min"
}
