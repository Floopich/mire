package webview

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/floopich/mire/internal/events"
	"github.com/floopich/mire/internal/modem"
	"github.com/floopich/mire/internal/service"
)

// « Proposer mon modem » : Mire prepare un rapport anonymise, l'affiche pour
// relecture, et ouvre le formulaire du depot prerempli.

const reportTimeout = 3 * time.Minute

// Le rapport prend une minute ou deux : il se prepare en tache de fond, et
// la page se recharge seule en montrant l'etape en cours, sans script.

// reportKeep est la duree pendant laquelle un rapport prepare reste
// consultable.
const reportKeep = 30 * time.Minute

// ReportResult est un rapport pret a envoyer.
type ReportResult struct {
	// Summary est le texte de la demande ; Full, le rapport complet lisible.
	Summary  string
	Full     string
	Download template.URL
	FileName string
	Issue    string
	// Name est le modem tel que la demande le nomme.
	Name string
}

type reportJob struct {
	address  string
	started  time.Time
	name     string // le modem tel que l'abonne l'a nomme
	operator string

	mu          sync.Mutex
	done, total int
	step        string
	report      modem.Report
	result      *ReportResult
}

func (j *reportJob) progress(done, total int, step string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.done, j.total, j.step = done, total, step
}

func (j *reportJob) snapshot() (done, total int, step string, result *ReportResult) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.done, j.total, j.step, j.result
}

// reportJobs garde les rapports en cours et recents, en memoire : ils ne
// survivent pas a un redemarrage, et n'ont pas a le faire.
type reportJobs struct {
	mu   sync.Mutex
	jobs map[string]*reportJob
}

// start lance un rapport, ou rend celui qui tourne deja pour la meme
// adresse : deux lectures en parallele ne feraient que ralentir le modem.
func (r *reportJobs) start(address, name, operator string, now time.Time, run func(*reportJob)) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.jobs == nil {
		r.jobs = map[string]*reportJob{}
	}
	for id, j := range r.jobs {
		_, _, _, res := j.snapshot()
		switch {
		case now.Sub(j.started) > reportKeep:
			delete(r.jobs, id)
		case res == nil && j.address == address:
			return id
		}
	}
	var b [12]byte
	_, _ = rand.Read(b[:])
	id := hex.EncodeToString(b[:])
	j := &reportJob{address: address, name: name, operator: operator, started: now, step: "préparation"}
	r.jobs[id] = j
	go run(j)
	return id
}

func (r *reportJobs) get(id string) *reportJob {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.jobs[id]
}

type proposeView struct {
	common
	Heading   string
	Address   string
	Supported bool
	// Name est le modem tel que l'abonne le nomme, pour le titre de la
	// demande ; Placeholder, ce que Mire en a detecte.
	Name string
	// Pendant la preparation :
	Running  bool
	Step     string
	Done     int
	Position int
	Total    int
	Elapsed  string
	// Un rapport demande mais introuvable : expire, ou Mire a redemarre.
	Expired bool
	// Apres preparation :
	Ready    bool
	ReportID string
	ReportResult
}

// proposeAddress retient l'adresse demandee si c'est une adresse HTTP,
// celle des reglages sinon.
func proposeAddress(r *http.Request, st service.State) string {
	raw := strings.TrimSpace(r.FormValue("adresse"))
	if u, err := url.Parse(raw); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
		return strings.TrimRight(raw, "/")
	}
	return st.Address
}

func (s *Server) proposeViewFor(r *http.Request) (proposeView, service.State) {
	st := s.opts.Service.State()
	v := proposeView{Address: proposeAddress(r, st)}
	v.Supported = st.Modem != nil && v.Address == st.Address
	v.Heading = "Proposer mon modem"
	if v.Supported {
		v.Heading = "Rapport du modem"
		v.Name = st.Model.Name
	}
	v.common = s.commonFor(r, v.Heading)
	return v, st
}

func (s *Server) handlePropose(w http.ResponseWriter, r *http.Request) {
	v, _ := s.proposeViewFor(r)
	if id := r.URL.Query().Get("rapport"); id != "" {
		j := s.reports.get(id)
		if j == nil {
			v.Expired = true
			s.render(w, http.StatusOK, "proposer", v)
			return
		}
		v.Address = j.address
		done, total, step, res := j.snapshot()
		if res == nil {
			v.Running, v.Step, v.Done, v.Position, v.Total = true, step, done, done+1, total
			if d := s.opts.Now().Sub(j.started); d < time.Minute {
				v.Elapsed = strconv.Itoa(int(d.Seconds())) + nbsp + "s"
			} else {
				v.Elapsed = events.Duration(d)
			}
			v.Refresh = 2
		} else {
			v.Ready, v.ReportResult, v.ReportID = true, *res, id
			// Le nom se corrige sur la page : la demande se refait avec.
			if name := strings.TrimSpace(r.URL.Query().Get("modele")); name != "" {
				j.mu.Lock()
				rep, operator := j.report, j.operator
				j.mu.Unlock()
				v.Name = clipName(name)
				v.Issue = modem.IssueLink(rep, v.Name, operator)
			} else {
				v.Name = res.Name
			}
		}
	}
	s.render(w, http.StatusOK, "proposer", v)
}

func (s *Server) handleProposeBuild(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "origine refusée", http.StatusForbidden)
		return
	}
	v, st := s.proposeViewFor(r)
	if v.Address == "" {
		http.Redirect(w, r, "/modem/proposer", http.StatusSeeOther)
		return
	}
	var driver modem.Driver
	model := ""
	if v.Supported {
		driver = st.Modem
		model = st.Model.ID
	}
	operator := st.Profile.Name
	if st.Profile.Region != "" {
		operator += ", " + st.Profile.Region
	}
	name := clipName(r.FormValue("modele"))
	id := s.reports.start(v.Address, name, operator, s.opts.Now(), func(j *reportJob) {
		// Le rapport survit a la requete : l'utilisateur peut fermer la page.
		ctx, cancel := context.WithTimeout(context.Background(), reportTimeout)
		defer cancel()
		rep := modem.BuildReport(ctx, j.address, driver, j.progress)
		rep.Mire, rep.Model = s.opts.Version, model
		full := modem.Readable(rep)
		name := firstNonEmptyString(j.name, modem.DetectedName(rep))
		res := &ReportResult{
			Summary: modem.Summary(rep, 6000),
			Full:    full,
			// Un lien de telechargement en data: : rien n'est ecrit sur le disque.
			Download: template.URL("data:text/plain;charset=utf-8;base64," + base64.StdEncoding.EncodeToString([]byte(full))),
			FileName: "mire-modem-" + s.opts.Now().Format("2006-01-02") + ".txt",
			Issue:    modem.IssueLink(rep, name, j.operator),
			Name:     name,
		}
		j.mu.Lock()
		j.report, j.result = rep, res
		j.mu.Unlock()
	})
	http.Redirect(w, r, "/modem/proposer?rapport="+id, http.StatusSeeOther)
}

// clipName borne le nom saisi : il devient le titre de la demande.
func clipName(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 80 {
		s = string(r[:80])
	}
	return s
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
