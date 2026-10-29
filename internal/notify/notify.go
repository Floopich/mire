// Package notify envoie les evenements du journal vers un service de
// notification : ntfy, Gotify, Discord, Telegram, Slack, mail ou webhook.
//
// Les notifications partent du journal, pas des detecteurs : une seule source
// de verite, et rien n'est perdu quand le service redemarre ou qu'un envoi
// echoue. Le notificateur lit les evenements nouveaux a intervalle court,
// retient le dernier traite, et reessaie tant que l'evenement a moins d'une
// heure : une alerte plus ancienne n'avertirait plus de rien.
package notify

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/events"
	"github.com/floopich/mire/internal/store"
)

// Message est une notification prete a envoyer.
type Message struct {
	Title string
	Body  string
	Level analyze.Level
	// Link mene au journal de l'instance, vide si son adresse est inconnue.
	Link string
	Time time.Time
	// Kinds sont les types d'evenements notifies, pour un webhook.
	Kinds []string
}

// Sender envoie une notification.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// Filtres d'evenements.
const (
	FilterState    = "etat"
	FilterCritical = "critiques"
	FilterAll      = "tout"
)

// stateKinds sont les evenements du filtre « etat de la ligne » : ceux qui
// changent ce que vit l'abonne, et leur fin.
var stateKinds = map[string]bool{
	events.KindState: true, events.KindOutage: true, events.KindOutageEnd: true,
	events.KindModemDown: true, events.KindModemUp: true,
}

// Wanted dit si un evenement passe le filtre.
func Wanted(e store.Event, filter string) bool {
	// Une note vient de l'utilisateur : la lui renvoyer n'apprend rien.
	if e.Kind == store.NoteKind {
		return false
	}
	switch filter {
	case FilterAll:
		return true
	case FilterCritical:
		return e.Level >= int(analyze.Critical)
	default:
		return stateKinds[e.Kind]
	}
}

const (
	// maxLines borne une notification groupee : au-dela, le journal en dit
	// plus qu'un message.
	maxLines = 8
	// maxAge est l'age au-dela duquel un evenement n'est plus envoye.
	maxAge = time.Hour
)

// Compose rassemble des evenements en une notification.
func Compose(evs []store.Event, link string) Message {
	m := Message{Link: link, Level: analyze.Unknown}
	for _, e := range evs {
		if analyze.Level(e.Level) > m.Level {
			m.Level = analyze.Level(e.Level)
		}
		m.Kinds = append(m.Kinds, e.Kind)
		if e.Time.After(m.Time) {
			m.Time = e.Time
		}
	}
	switch m.Level {
	case analyze.Critical:
		m.Title = "Mire : alerte critique"
	case analyze.Warning:
		m.Title = "Mire : à surveiller"
	case analyze.Good, analyze.Tolerated:
		m.Title = "Mire : retour à la normale"
	default:
		m.Title = "Mire : information"
	}
	if len(evs) == 1 {
		m.Body = evs[0].Message
	} else {
		var lines []string
		for i, e := range evs {
			if i == maxLines {
				lines = append(lines, "… et "+strconv.Itoa(len(evs)-maxLines)+" autres événements.")
				break
			}
			lines = append(lines, e.Time.Local().Format("15:04")+" · "+e.Message)
		}
		m.Body = strings.Join(lines, "\n")
	}
	if link != "" {
		m.Link = strings.TrimRight(link, "/") + "/evenements"
	}
	return m
}

// Notifier envoie les nouveaux evenements du journal.
type Notifier struct {
	DB     *sql.DB
	Sender Sender
	Filter string
	Link   string
	Logger *slog.Logger
	// Every est l'intervalle de lecture du journal ; 15 s par defaut.
	Every time.Duration
	Now   func() time.Time

	failing bool
}

// lastKey retient le dernier evenement traite. Il est hors du registre des
// reglages : c'est un etat, pas un choix de l'utilisateur.
const lastKey = "notif.dernier_evenement"

// Run lit le journal jusqu'a l'annulation du contexte.
func (n *Notifier) Run(ctx context.Context) error {
	every := n.Every
	if every <= 0 {
		every = 15 * time.Second
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		n.Poll(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Poll envoie les evenements inscrits depuis le dernier passage.
func (n *Notifier) Poll(ctx context.Context) {
	if err := n.poll(ctx); err != nil && ctx.Err() == nil {
		n.logger().Error("notifications", "erreur", err)
	}
}

func (n *Notifier) poll(ctx context.Context) error {
	raw, err := store.Setting(ctx, n.DB, lastKey, "")
	if err != nil {
		return err
	}
	if raw == "" {
		// Premiere mise en service : le passe n'est pas envoye.
		id, err := store.LastEventID(ctx, n.DB)
		if err != nil {
			return err
		}
		return store.SetSetting(ctx, n.DB, lastKey, strconv.FormatInt(id, 10))
	}
	last, _ := strconv.ParseInt(raw, 10, 64)

	now := time.Now
	if n.Now != nil {
		now = n.Now
	}
	var batch []store.Event
	next := last
	for {
		list, err := store.EventsAfter(ctx, n.DB, next, 500)
		if err != nil {
			return err
		}
		for _, e := range list {
			if now().Sub(e.Time) <= maxAge && Wanted(e, n.Filter) {
				batch = append(batch, e)
			}
			next = e.ID
		}
		if len(list) < 500 {
			break
		}
	}
	if next == last {
		return nil
	}
	if len(batch) > 0 {
		if err := n.Sender.Send(ctx, Compose(batch, n.Link)); err != nil {
			// L'envoi sera retente au passage suivant ; un seul message
			// d'erreur par serie d'echecs.
			if !n.failing {
				n.logger().Warn("notification non envoyée, nouvel essai dans un instant", "erreur", err)
			}
			n.failing = true
			return nil
		}
		if n.failing {
			n.logger().Info("notifications rétablies")
		}
		n.failing = false
	}
	return store.SetSetting(ctx, n.DB, lastKey, strconv.FormatInt(next, 10))
}

func (n *Notifier) logger() *slog.Logger {
	if n.Logger != nil {
		return n.Logger
	}
	return slog.Default()
}

// Test envoie une notification d'essai.
func Test(ctx context.Context, s Sender, link string) error {
	m := Compose([]store.Event{{Time: time.Now(), Kind: "essai", Level: int(analyze.Good),
		Message: "Notification d'essai : Mire peut vous prévenir."}}, link)
	m.Title = "Mire : essai"
	return s.Send(ctx, m)
}

// tag rend le niveau d'un message en un mot, pour un webhook.
func tag(l analyze.Level) string {
	switch l {
	case analyze.Critical:
		return "critique"
	case analyze.Warning:
		return "avertissement"
	case analyze.Good, analyze.Tolerated:
		return "bon"
	default:
		return "information"
	}
}

// statusError resume une reponse HTTP en echec sans en recopier le corps,
// qui pourrait renvoyer un jeton.
func statusError(service string, code int) error {
	return fmt.Errorf("%s a répondu HTTP %d", service, code)
}
