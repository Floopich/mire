package webview

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/floopich/mire/internal/events"
)

// Une action lente (lire le modem, essayer un service) tourne en tache de
// fond. La reponse attend un court instant : une action rapide s'affiche
// sans detour. Au-dela, la page montre l'etape en cours et se recharge seule
// jusqu'au resultat, sans script.

// quickWait est l'attente avant d'afficher la progression ; les tests la
// raccourcissent.
var quickWait = 1500 * time.Millisecond

const (
	// workKeep est la duree pendant laquelle un resultat reste consultable.
	workKeep = 10 * time.Minute
	// workRefresh est le rythme de rechargement de la page d'attente.
	workRefresh = 2
)

// work est une action en cours ou terminee.
type work struct {
	started time.Time
	done    chan struct{}

	mu       sync.Mutex
	step     string
	result   any
	finished time.Time
}

// setStep nomme l'etape en cours, en langage courant.
func (w *work) setStep(step string) {
	w.mu.Lock()
	w.step = step
	w.mu.Unlock()
}

// wait attend la fin au plus d et dit si l'action est terminee.
func (w *work) wait(d time.Duration) bool {
	select {
	case <-w.done:
		return true
	case <-time.After(d):
		return false
	}
}

// state renvoie l'etape en cours, ou le resultat une fois termine.
func (w *work) state() (step string, result any, finished bool) {
	select {
	case <-w.done:
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.step, w.result, true
	default:
		w.mu.Lock()
		defer w.mu.Unlock()
		return w.step, nil, false
	}
}

// elapsed rend la duree ecoulee depuis le debut : « 12 s », « 1 min ».
func (w *work) elapsed(now time.Time) string {
	d := now.Sub(w.started)
	if d < time.Minute {
		return strconv.Itoa(int(d.Seconds())) + nbsp + "s"
	}
	return events.Duration(d)
}

// works garde les actions recentes, en memoire.
type works struct {
	mu sync.Mutex
	m  map[string]*work
}

// start lance une action, bornee par timeout, et renvoie son identifiant.
// L'action ne depend pas de la requete qui la lance : elle continue si le
// navigateur passe a la page d'attente.
func (ws *works) start(timeout time.Duration, step string, fn func(ctx context.Context, w *work) any) (string, *work) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if ws.m == nil {
		ws.m = map[string]*work{}
	}
	now := time.Now()
	for id, w := range ws.m {
		if now.Sub(w.started) > workKeep {
			delete(ws.m, id)
		}
	}
	var b [12]byte
	_, _ = rand.Read(b[:])
	id := hex.EncodeToString(b[:])
	w := &work{started: now, done: make(chan struct{}), step: step}
	ws.m[id] = w
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		res := fn(ctx, w)
		w.mu.Lock()
		w.result, w.finished = res, time.Now()
		w.mu.Unlock()
		close(w.done)
	}()
	return id, w
}

// get renvoie une action, nil si elle est inconnue ou expiree.
func (ws *works) get(id string) *work {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	return ws.m[id]
}

// pending est une action en cours, telle que la page l'affiche.
type pending struct {
	Step    string
	Elapsed string
}

// readingFresh est la duree pendant laquelle une lecture terminee s'affiche
// encore telle quelle : au-dela, recharger la page relit.
const readingFresh = time.Minute

// awaited sert une page qui attend une lecture : il la lance, ou retrouve
// celle que nomme le parametre param. Il renvoie son resultat une fois
// terminee, ou l'etape en cours ; redirected dit qu'il a deja repondu en
// renvoyant vers l'adresse qui suit la lecture.
func (s *Server) awaited(w http.ResponseWriter, r *http.Request, param string, timeout time.Duration, step string,
	fn func(ctx context.Context, w *work) any) (result any, wait *pending, redirected bool) {
	if wk := s.works.get(r.URL.Query().Get(param)); wk != nil {
		step, res, done := wk.state()
		if !done {
			return nil, &pending{Step: step, Elapsed: wk.elapsed(time.Now())}, false
		}
		wk.mu.Lock()
		fresh := time.Since(wk.finished) < readingFresh
		wk.mu.Unlock()
		if fresh {
			return res, nil, false
		}
	}
	id, wk := s.works.start(timeout, step, fn)
	if wk.wait(quickWait) {
		_, res, _ := wk.state()
		return res, nil, false
	}
	q := r.URL.Query()
	q.Set(param, id)
	u := *r.URL
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.RequestURI(), http.StatusSeeOther)
	return nil, nil, true
}
