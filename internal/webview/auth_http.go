package webview

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
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
	key := s.clientKey(r)

	// Le SameSite=Strict du cookie bloque deja la soumission depuis un autre
	// site ; on refuse en plus une origine etrangere, qui n'a aucune raison
	// d'exister sur un formulaire local.
	if !sameOrigin(r) {
		http.Error(w, "origine refusée", http.StatusForbidden)
		return
	}
	// La tentative est comptee avant la verification : des requetes
	// simultanees ne passent pas toutes avant le premier echec.
	if wait := s.limiter.Reserve(key); wait > 0 {
		s.renderLogin(w, http.StatusTooManyRequests,
			"Trop de tentatives. Réessayez dans "+humanDelay(wait)+".")
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "formulaire illisible", http.StatusBadRequest)
		return
	}

	ok, err := s.checkPassword(ctx, r.PostFormValue("mot_de_passe"))
	if err != nil || !ok {
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

// maxVerifying borne les verifications de mot de passe simultanees : chacune
// occupe un coeur un instant, et une rafale ne doit pas affamer la collecte.
const maxVerifying = 2

// checkPassword verifie le mot de passe de l'interface, au plus
// maxVerifying a la fois.
func (s *Server) checkPassword(ctx context.Context, password string) (bool, error) {
	select {
	case s.verifying <- struct{}{}:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	defer func() { <-s.verifying }()
	return auth.Check(ctx, s.db, password)
}

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
		HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteStrictMode, MaxAge: -1,
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

// clientKey identifie l'appelant pour le limiteur par l'adresse de la
// connexion. X-Forwarded-For n'est lu que si la connexion vient d'un proxy
// declare de confiance (MIRE_PROXY_DE_CONFIANCE) : sans cela, derriere un
// proxy inverse, tout le monde partagerait la meme adresse, et trois fautes
// de frappe bloqueraient chacun. L'en-tete se lit de droite a gauche : la
// premiere adresse qui n'est pas un proxy de confiance est le client, et ce
// qu'un client ecrit lui-meme a gauche n'est jamais retenu.
func (s *Server) clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !s.trusted(host) {
		return host
	}
	var hops []string
	for _, h := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(h, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		hop := strings.TrimSpace(hops[i])
		ip, err := netip.ParseAddr(hop)
		if err != nil {
			break
		}
		if !s.trusted(hop) {
			return ip.Unmap().String()
		}
	}
	return host
}

// trusted dit si une adresse est un proxy de confiance.
func (s *Server) trusted(addr string) bool {
	ip, err := netip.ParseAddr(addr)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	for _, p := range s.opts.TrustedProxies {
		if p.Contains(ip) {
			return true
		}
	}
	return false
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
