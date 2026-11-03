package webview

import (
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Remonter dans le temps : une page a fenetre peut s'arreter avant
// maintenant (?fin=2026-11-03T20:00, heure locale). L'adresse se partage, et
// les boutons passent a la periode precedente ou suivante.

// finLayout est le format du parametre fin, en heure locale.
const finLayout = "2006-01-02T15:04"

type period struct {
	From, To time.Time
	// Live dit que la periode finit maintenant.
	Live bool
	// Fin est la valeur du parametre, vide pour maintenant.
	Fin string
	// Prev, Next et Now sont les adresses des periodes voisines ; Next et
	// Now sont vides quand la periode finit maintenant.
	Prev, Next, Now string
	// Label decrit une periode passee : « du 27/10 à 20:00 au 03/11 à
	// 20:00 ».
	Label string
}

// periodFor lit la periode demandee sur path. Une fin future, ou illisible,
// revient a maintenant.
func periodFor(r *http.Request, path string, hours int, now time.Time) period {
	length := time.Duration(hours) * time.Hour
	p := period{To: now, Live: true}
	if v := r.URL.Query().Get("fin"); v != "" {
		if end, err := time.ParseInLocation(finLayout, v, time.Local); err == nil && end.Before(now) {
			p.To, p.Live, p.Fin = end, false, end.Format(finLayout)
		}
	}
	p.From = p.To.Add(-length)
	link := func(end time.Time) string {
		q := url.Values{"heures": {strconv.Itoa(hours)}}
		if end.Before(now) {
			q.Set("fin", end.Local().Format(finLayout))
		}
		return path + "?" + q.Encode()
	}
	// La periode precedente finit ou celle-ci commence, a la minute pres.
	p.Prev = link(p.From.Truncate(time.Minute))
	if !p.Live {
		p.Next = link(p.To.Add(length).Truncate(time.Minute))
		p.Now = path + "?heures=" + strconv.Itoa(hours)
		p.Label = "du " + p.From.Local().Format("02/01/2006 à 15:04") + " au " + p.To.Local().Format("02/01/2006 à 15:04")
	}
	return p
}
