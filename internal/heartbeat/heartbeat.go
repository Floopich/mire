// Package heartbeat envoie le signal de vie : un appel a une adresse apres
// chaque releve reussi. Les outils qui l'attendent (sonde Push d'Uptime
// Kuma, Healthchecks.io...) alertent quand il ne vient plus : Mire arrete,
// modem muet ou collecte bloquee.
package heartbeat

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/floopich/mire/internal/docsis"
)

// Timeout borne un appel : un outil lent ne retarde jamais la collecte.
const Timeout = 10 * time.Second

// Pinger appelle URL apres chaque releve enregistre. Il implemente
// l'observateur de la collecte.
type Pinger struct {
	URL       string
	UserAgent string
	Client    *http.Client
	Logger    *slog.Logger

	busy   atomic.Bool
	failed atomic.Bool
}

// Recorded envoie le signal de vie, sans attendre la reponse.
func (p *Pinger) Recorded(_ context.Context, _ docsis.Snapshot) {
	if p.URL == "" || !p.busy.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer p.busy.Store(false)
		_ = p.Ping(context.Background())
	}()
}

// Failed ne fait rien : c'est l'absence de signal qui alerte.
func (p *Pinger) Failed(context.Context, error) {}

// Ping envoie un signal de vie et rend l'erreur eventuelle. Le journal ne
// cite que le nom de l'hote : le chemin porte souvent un jeton.
func (p *Pinger) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	err := p.do(ctx)
	host := ""
	if u, perr := url.Parse(p.URL); perr == nil {
		host = u.Host
	}
	switch {
	case err != nil && !p.failed.Swap(true):
		p.logger().Warn("signal de vie refusé", "hote", host, "erreur", err)
	case err == nil && p.failed.Swap(false):
		p.logger().Info("signal de vie de nouveau accepté", "hote", host)
	}
	return err
}

func (p *Pinger) do(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL, nil)
	if err != nil {
		return errors.New("adresse invalide")
	}
	if p.UserAgent != "" {
		req.Header.Set("User-Agent", p.UserAgent)
	}
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: Timeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return ue.Err // sans l'adresse, qui peut porter un jeton
		}
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 300 {
		return errors.New("réponse HTTP " + strconv.Itoa(resp.StatusCode))
	}
	return nil
}

func (p *Pinger) logger() *slog.Logger {
	if p.Logger != nil {
		return p.Logger
	}
	return slog.Default()
}
