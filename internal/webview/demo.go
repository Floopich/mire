package webview

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/floopich/mire/internal/auth"
)

// Tant que Mire n'est pas installe, l'accueil propose d'essayer une ligne de
// demonstration. Elle tourne a cote de l'instance, dans sa propre base
// jetable : le navigateur qui l'a ouverte la voit a la place de l'assistant,
// par un cookie, jusqu'a ce qu'il la quitte. Choisir le mot de passe de
// l'instance l'arrete.

const (
	demoCookie = "mire_demo"
	// demoTimeout borne la preparation : l'historique s'ecrit en quelques
	// secondes, une minute sur une petite machine.
	demoTimeout = 3 * time.Minute
)

type demoState struct {
	mu      sync.Mutex
	handler http.Handler
	close   func()
	// work est la preparation en cours.
	work string
}

// demoHandler renvoie la demonstration en cours, nil sans demonstration.
func (s *Server) demoHandler() http.Handler {
	s.demo.mu.Lock()
	defer s.demo.mu.Unlock()
	return s.demo.handler
}

// stopDemo arrete la demonstration et efface sa base.
func (s *Server) stopDemo() {
	s.demo.mu.Lock()
	closeDemo := s.demo.close
	s.demo.handler, s.demo.close, s.demo.work = nil, nil, ""
	s.demo.mu.Unlock()
	if closeDemo != nil {
		closeDemo()
	}
}

// Close arrete ce que le serveur a lance : la demonstration.
func (s *Server) Close() { s.stopDemo() }

// withDemo sert la demonstration au navigateur qui l'a ouverte. Les adresses
// /demo restent a l'instance : c'est par elles qu'on en sort.
func (s *Server) withDemo(next http.Handler) http.Handler {
	if s.opts.StartDemo == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/demo" && !strings.HasPrefix(r.URL.Path, "/demo/") {
			if c, err := r.Cookie(demoCookie); err == nil && c.Value == "1" {
				if h := s.demoHandler(); h != nil {
					h.ServeHTTP(w, r)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func setDemoCookie(w http.ResponseWriter, r *http.Request, on bool) {
	c := &http.Cookie{Name: demoCookie, Value: "1", Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: isHTTPS(r)}
	if !on {
		c.Value, c.MaxAge = "", -1
	}
	http.SetCookie(w, c)
}

// installed dit si le mot de passe de l'instance existe : la demonstration
// n'est alors plus proposee.
func (s *Server) installed(r *http.Request) bool {
	has, err := auth.HasPassword(r.Context(), s.db)
	return err != nil || has
}

// handleDemoStart prepare la demonstration, ou rejoint celle qui tourne ou
// se prepare.
func (s *Server) handleDemoStart(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "origine refusée", http.StatusForbidden)
		return
	}
	if s.opts.StartDemo == nil || s.installed(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	setDemoCookie(w, r, true)
	s.demo.mu.Lock()
	if s.demo.handler != nil {
		s.demo.mu.Unlock()
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	id := s.demo.work
	wk := s.works.get(id)
	if _, res, done := stateOf(wk); wk == nil || (done && res != nil) {
		id, wk = s.works.start(demoTimeout, "Préparation de la démonstration",
			func(ctx context.Context, _ *work) any {
				h, closeDemo, err := s.opts.StartDemo(ctx)
				if err != nil {
					s.opts.Logger.Error("démonstration", "erreur", err)
					return err
				}
				s.demo.mu.Lock()
				s.demo.handler, s.demo.close = h, closeDemo
				s.demo.mu.Unlock()
				return nil
			})
		s.demo.work = id
	}
	s.demo.mu.Unlock()
	if wk.wait(quickWait) {
		if _, res, _ := wk.state(); res == nil {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, "/demo?preparation="+id, http.StatusSeeOther)
}

func stateOf(wk *work) (string, any, bool) {
	if wk == nil {
		return "", nil, false
	}
	return wk.state()
}

// handleDemoWait suit la preparation, puis ouvre la demonstration.
func (s *Server) handleDemoWait(w http.ResponseWriter, r *http.Request) {
	wk := s.works.get(r.URL.Query().Get("preparation"))
	if wk == nil || s.opts.StartDemo == nil {
		http.Redirect(w, r, "/installation", http.StatusSeeOther)
		return
	}
	step, res, done := wk.state()
	v := s.setupView("demo", 0)
	switch {
	case !done:
		v.Searching, v.Refresh = &pending{Step: step, Elapsed: wk.elapsed(time.Now())}, workRefresh
	case res != nil:
		v.Step, v.DemoOffered = "accueil", true
		msg := "erreur inconnue"
		if err, ok := res.(error); ok {
			msg = err.Error()
		}
		v.Err = "La démonstration n'a pas pu démarrer : " + msg + "."
	default:
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, http.StatusOK, "setup", v)
}

// handleDemoQuit quitte la demonstration pour installer Mire.
func (s *Server) handleDemoQuit(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "origine refusée", http.StatusForbidden)
		return
	}
	setDemoCookie(w, r, false)
	s.stopDemo()
	http.Redirect(w, r, "/installation?etape=mot-de-passe", http.StatusSeeOther)
}
