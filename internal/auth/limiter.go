package auth

import (
	"sync"
	"time"
)

// Limiter ralentit les tentatives de connexion par adresse : le delai
// double a chaque echec a partir du troisieme, et retombe a zero apres une
// connexion reussie.
type Limiter struct {
	mu       sync.Mutex
	failures map[string]*entry
	now      func() time.Time
}

type entry struct {
	count int
	until time.Time
	seen  time.Time
}

const (
	freeAttempts = 3
	baseDelay    = 5 * time.Second
	maxDelay     = 15 * time.Minute
	forgetAfter  = time.Hour
)

func NewLimiter() *Limiter {
	return &Limiter{failures: map[string]*entry{}, now: time.Now}
}

// Wait renvoie le temps a attendre avant la prochaine tentative, zero si libre.
func (l *Limiter) Wait(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.failures[key]
	if !ok {
		return 0
	}
	if d := e.until.Sub(l.now()); d > 0 {
		return d
	}
	return 0
}

// Reserve compte une tentative avant sa verification, d'un seul tenant :
// des requetes envoyees en meme temps ne passent pas toutes avant le premier
// echec enregistre. Il renvoie le temps a attendre, zero si la tentative
// peut avoir lieu ; une tentative reussie efface ensuite l'historique
// (Succeed), un echec n'a plus rien a ajouter.
func (l *Limiter) Reserve(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.failures[key]; ok {
		if d := e.until.Sub(l.now()); d > 0 {
			return d
		}
	}
	l.fail(key)
	return 0
}

func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fail(key)
}

func (l *Limiter) fail(key string) {
	l.forgetOld()
	e, ok := l.failures[key]
	if !ok {
		e = &entry{}
		l.failures[key] = e
	}
	e.count++
	e.seen = l.now()
	if e.count < freeAttempts {
		return
	}
	delay := baseDelay << (e.count - freeAttempts)
	if delay > maxDelay || delay <= 0 {
		delay = maxDelay
	}
	e.until = l.now().Add(delay)
}

// Succeed efface l'historique d'une adresse.
func (l *Limiter) Succeed(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, key)
}

// forgetOld borne la memoire : sans elle, une rafale d'adresses differentes
// ferait grossir la table indefiniment.
func (l *Limiter) forgetOld() {
	cutoff := l.now().Add(-forgetAfter)
	for k, e := range l.failures {
		if e.seen.Before(cutoff) && e.until.Before(l.now()) {
			delete(l.failures, k)
		}
	}
}
