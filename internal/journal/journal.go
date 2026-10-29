// Package journal garde en memoire les dernieres lignes du journal du
// service, pour la page Reglages, et porte son niveau de detail.
package journal

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Capacity est le nombre de lignes gardees.
const Capacity = 500

// Niveaux de detail, tels que les reglages les nomment.
const (
	Errors   = "erreurs"
	Normal   = "normal"
	Detailed = "detaille"
)

// Entry est une ligne du journal.
type Entry struct {
	Time    time.Time
	Level   slog.Level
	Message string
	Attrs   string
}

// Journal recoit les lignes du service et les garde en memoire.
type Journal struct {
	mu    sync.Mutex
	lines []Entry
	next  int
	level slog.LevelVar
}

// New cree un journal qui ecrit aussi sur w, au format texte de slog.
func New(w io.Writer) (*Journal, *slog.Logger) {
	j := &Journal{lines: make([]Entry, 0, Capacity)}
	inner := slog.NewTextHandler(w, &slog.HandlerOptions{Level: &j.level})
	return j, slog.New(&handler{j: j, inner: inner})
}

// SetLevel change le niveau de detail, aussitot.
func (j *Journal) SetLevel(name string) { j.level.Set(Level(name)) }

// Level traduit un niveau des reglages.
func Level(name string) slog.Level {
	switch name {
	case Errors:
		return slog.LevelWarn
	case Detailed:
		return slog.LevelDebug
	default:
		return slog.LevelInfo
	}
}

// Entries rend les lignes gardees, de la plus recente a la plus ancienne.
func (j *Journal) Entries() []Entry {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]Entry, 0, len(j.lines))
	for i := 0; i < len(j.lines); i++ {
		k := (j.next - 1 - i + len(j.lines)) % len(j.lines)
		out = append(out, j.lines[k])
	}
	return out
}

func (j *Journal) add(e Entry) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.lines) < Capacity {
		j.lines = append(j.lines, e)
		j.next = len(j.lines) % Capacity
		return
	}
	j.lines[j.next] = e
	j.next = (j.next + 1) % Capacity
}

type handler struct {
	j      *Journal
	inner  slog.Handler
	attrs  []slog.Attr
	prefix string
}

func (h *handler) Enabled(ctx context.Context, l slog.Level) bool { return h.inner.Enabled(ctx, l) }

func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	var b strings.Builder
	write := func(a slog.Attr) {
		if a.Equal(slog.Attr{}) {
			return
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(h.prefix + a.Key + "=")
		v := a.Value.Resolve().String()
		if strings.ContainsAny(v, " \"=") || v == "" {
			v = strconv.Quote(v)
		}
		b.WriteString(v)
	}
	for _, a := range h.attrs {
		write(a)
	}
	r.Attrs(func(a slog.Attr) bool { write(a); return true })
	h.j.add(Entry{Time: r.Time, Level: r.Level, Message: r.Message, Attrs: b.String()})
	return h.inner.Handle(ctx, r)
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	n := *h
	n.inner = h.inner.WithAttrs(attrs)
	n.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &n
}

func (h *handler) WithGroup(name string) slog.Handler {
	n := *h
	n.inner = h.inner.WithGroup(name)
	n.prefix = h.prefix + name + "."
	return &n
}
