package webview

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/floopich/mire/internal/auth"
	"github.com/floopich/mire/internal/modem"
	"github.com/floopich/mire/internal/settings"
)

// L'assistant de premier demarrage tient en trois etapes : le mot de passe
// de l'interface, l'operateur, le modem. L'operateur vient avant le modem :
// il donne l'adresse et le modele les plus probables. Chaque etape enregistre ses reglages
// au fur et a mesure : un abandon en cours de route laisse une instance
// utilisable, a completer depuis la page Reglages.

type setupView struct {
	common
	Step        string
	StepNumber  int
	Err         string
	Detected    []modem.Candidate
	Address     string
	Model       string
	User        string
	DefaultUser string
	Models      []settings.Option
	Profiles    []settings.Option
	ProfileID   string
	Interval    string
	Test        *testResult
	// Testing est l'essai en cours ; Searching, la recherche du modem.
	Testing     *pending
	Searching   *pending
	PasswordSet bool
	// Unsupported : « Autre modem » choisi, la sonde seule fonctionne.
	Unsupported bool
	// DemoOffered propose d'essayer la demonstration, a l'accueil.
	DemoOffered bool
}

func (s *Server) setupView(step string, number int) setupView {
	st := s.opts.Service.State()
	v := setupView{common: s.bareCommon("Installation"), Step: step, StepNumber: number}
	modelDef, _ := settings.Lookup(settings.ModemModel)
	profileDef, _ := settings.Lookup(settings.Profile)
	v.Models = modelDef.Options()
	v.Profiles = profileDef.Options()
	v.Address = st.Values.String(settings.ModemURL)
	v.Model = st.Values.String(settings.ModemModel)
	if v.Model == "" {
		v.Model = effectiveModel(st)
	}
	v.User = st.Values.String(settings.ModemUser)
	v.DefaultUser = st.Model.DefaultUser
	v.ProfileID = st.Profile.ID
	v.Interval = st.Values.String(settings.Interval)
	v.PasswordSet = st.Values.String(settings.ModemPassword) != ""
	return v
}

// handleSetupStart montre la premiere etape, tant qu'aucun mot de passe
// n'existe.
func (s *Server) handleSetupStart(w http.ResponseWriter, r *http.Request) {
	if s.opts.Demo {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	has, err := auth.HasPassword(r.Context(), s.db)
	if err != nil {
		s.fail(w, err)
		return
	}
	if has {
		target := "/connexion"
		if s.loggedIn(r) {
			target = "/installation/operateur"
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	// L'accueil propose d'abord d'essayer Mire sur une ligne fictive.
	if s.opts.StartDemo != nil && r.URL.Query().Get("etape") != "mot-de-passe" {
		v := s.setupView("accueil", 0)
		v.DemoOffered = true
		s.render(w, http.StatusOK, "setup", v)
		return
	}
	s.render(w, http.StatusOK, "setup", s.setupView("mot-de-passe", 1))
}

func (s *Server) handleSetupPassword(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "origine refusée", http.StatusForbidden)
		return
	}
	// Une fois le mot de passe choisi, cette etape est fermee : sinon,
	// n'importe qui sur le reseau pourrait le remplacer.
	if has, err := auth.HasPassword(r.Context(), s.db); err != nil || has {
		http.Redirect(w, r, "/connexion", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "formulaire illisible", http.StatusBadRequest)
		return
	}
	v := s.setupView("mot-de-passe", 1)
	if r.PostForm.Get("nouveau") != r.PostForm.Get("confirmation") {
		v.Err = "Les deux saisies diffèrent."
		s.render(w, http.StatusUnprocessableEntity, "setup", v)
		return
	}
	if err := auth.SetInitialPassword(r.Context(), s.db, r.PostForm.Get("nouveau")); err != nil {
		if errors.Is(err, auth.ErrPasswordExists) {
			http.Redirect(w, r, "/connexion", http.StatusSeeOther)
			return
		}
		v.Err = capitalize(err.Error()) + "."
		s.render(w, http.StatusUnprocessableEntity, "setup", v)
		return
	}
	if err := s.openSession(w, r); err != nil {
		s.fail(w, err)
		return
	}
	// Mire est installe : la demonstration n'a plus lieu d'etre.
	setDemoCookie(w, r, false)
	s.stopDemo()
	// L'adresse par laquelle on ouvre Mire pour la premiere fois sert de
	// lien dans les notifications : une question de moins, modifiable
	// ensuite dans les reglages.
	if s.opts.Service.State().Values.String(settings.NotifyLink) == "" {
		_ = settings.Set(r.Context(), s.db, s.opts.Service.Forced(), map[string]string{settings.NotifyLink: origin(r)})
	}
	http.Redirect(w, r, "/installation/operateur", http.StatusSeeOther)
}

func (s *Server) handleSetupModem(w http.ResponseWriter, r *http.Request) {
	v := s.setupView("modem", 3)
	if id := r.URL.Query().Get("essai"); id != "" {
		if wk := s.works.get(id); wk != nil {
			step, res, done := wk.state()
			if !done {
				v.Testing, v.Refresh = &pending{Step: step, Elapsed: wk.elapsed(time.Now())}, workRefresh
			} else {
				v.Test = res.(*testResult)
			}
			s.render(w, http.StatusOK, "setup", v)
			return
		}
	}
	res, wait, redirected := s.awaited(w, r, "recherche", detectTimeout, "Recherche du modem sur le réseau", s.detect)
	switch {
	case redirected:
		return
	case wait != nil:
		v.Searching, v.Refresh = wait, workRefresh
		s.render(w, http.StatusOK, "setup", v)
		return
	}
	st := s.opts.Service.State()
	v.Detected = res.([]modem.Candidate)
	if v.Address == "" && len(v.Detected) > 0 {
		v.Address = v.Detected[0].URL
	}
	// Le modem reconnu decide du pilote ; un modem cable inconnu prend
	// « Autre modem » plutot qu'un pilote qui ne lui correspond pas.
	if st.Values.String(settings.ModemModel) == "" && len(v.Detected) > 0 {
		switch c := v.Detected[0]; {
		case c.Model != nil:
			v.Model = c.Model.ID
		case c.DOCSIS:
			v.Model = modem.Generic
		}
	}
	s.render(w, http.StatusOK, "setup", v)
}

func (s *Server) handleSetupModemSave(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "origine refusée", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "formulaire illisible", http.StatusBadRequest)
		return
	}
	address := r.PostForm.Get("adresse")
	if address == "autre" {
		address = strings.TrimSpace(r.PostForm.Get("adresse_autre"))
	}
	input := map[string]string{
		settings.ModemURL:      address,
		settings.ModemModel:    r.PostForm.Get("modele"),
		settings.ModemUser:     r.PostForm.Get("identifiant"),
		settings.ModemPassword: r.PostForm.Get("mot_de_passe"),
	}
	svc := s.opts.Service
	v := s.setupView("modem", 3)
	if err := settings.Set(r.Context(), s.db, svc.Forced(), input); err != nil {
		v.Err = err.Error()
		v.Address, v.Model, v.User = address, input[settings.ModemModel], input[settings.ModemUser]
		s.render(w, http.StatusUnprocessableEntity, "setup", v)
		return
	}
	if err := svc.Reload(r.Context()); err != nil {
		s.fail(w, err)
		return
	}
	v = s.setupView("modem", 3)
	if errors.Is(s.opts.Service.State().ModelErr, modem.ErrUnsupported) {
		v.Unsupported = true
		s.render(w, http.StatusOK, "setup", v)
		return
	}
	t := groupTests["modem"]
	id, wk := s.works.start(testTimeout, t.step, func(ctx context.Context, _ *work) any { return t.run(s, ctx) })
	if !wk.wait(quickWait) {
		http.Redirect(w, r, "/installation/modem?essai="+id, http.StatusSeeOther)
		return
	}
	_, res, _ := wk.state()
	v.Test = res.(*testResult)
	s.render(w, http.StatusOK, "setup", v)
}

func (s *Server) handleSetupOperator(w http.ResponseWriter, r *http.Request) {
	s.render(w, http.StatusOK, "setup", s.setupView("operateur", 2))
}

func (s *Server) handleSetupOperatorSave(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "origine refusée", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "formulaire illisible", http.StatusBadRequest)
		return
	}
	svc := s.opts.Service
	input := map[string]string{
		settings.Profile:  r.PostForm.Get("profil"),
		settings.Interval: r.PostForm.Get("intervalle"),
	}
	if err := settings.Set(r.Context(), s.db, svc.Forced(), input); err != nil {
		v := s.setupView("operateur", 2)
		v.Err = err.Error()
		s.render(w, http.StatusUnprocessableEntity, "setup", v)
		return
	}
	if err := svc.Reload(r.Context()); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/installation/modem", http.StatusSeeOther)
}

// origin rend l'adresse de Mire telle que le navigateur l'a ouverte,
// derriere un proxy inverse compris.
func origin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = strings.TrimSpace(strings.Split(h, ",")[0])
	}
	return scheme + "://" + host
}
