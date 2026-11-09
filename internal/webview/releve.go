package webview

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/floopich/mire/internal/analyze"
	"github.com/floopich/mire/internal/store"
)

// La page d'un releve montre la ligne a un instant du passe : le verdict de
// ce releve, ses raisons et chaque canal, juges avec les seuils actuels.

type releveView struct {
	common
	At         time.Time
	Verdict    verdict
	Reasons    []reason
	DownGroups []channelGroup
	UpGroups   []channelGroup
	// Prev et Next menent aux releves voisins ; Around a la chronologie
	// centree sur ce releve.
	Prev, Next, Around string
}

func releveURL(t time.Time) string {
	return "/releve?" + url.Values{"t": {t.Local().Format(finLayout)}}.Encode()
}

func (s *Server) handleReleve(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	at, err := time.ParseInLocation(finLayout, r.URL.Query().Get("t"), time.Local)
	if err != nil {
		s.renderError(w, http.StatusBadRequest, "Date invalide",
			"L'adresse doit se terminer par une date, par exemple /releve?t=2026-11-03T20:00.")
		return
	}
	// Le releve de cette minute, ou le dernier avant elle.
	snap, err := store.SnapshotBefore(ctx, s.db, at.Add(time.Minute))
	if errors.Is(err, sql.ErrNoRows) {
		s.renderError(w, http.StatusNotFound, "Aucun relevé",
			"Mire n'a aucun relevé à cette date ni avant.")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	st := s.opts.Service.State()
	v := releveView{common: s.commonFor(r, "Relevé du "+FmtTime(snap.Timestamp)), At: snap.Timestamp}
	h, err := st.Analyze(ctx, s.db, snap)
	if err != nil {
		s.fail(w, err)
		return
	}
	v.Verdict = verdictFor(st.Profile, true, h)
	v.Reasons = findingsOf(h)

	fin := url.Values{"heures": {"24"}, "fin": {snap.Timestamp.Add(12 * time.Hour).Local().Format(finLayout)}}
	v.Around = "/chronologie?" + fin.Encode()
	if prev, err := store.SnapshotBefore(ctx, s.db, snap.Timestamp); err == nil {
		v.Prev = releveURL(prev.Timestamp)
	}
	if next, err := store.SnapshotAfter(ctx, s.db, snap.Timestamp); err == nil {
		v.Next = releveURL(next.Timestamp)
	}

	// Les liens vers les canaux montrent les 24 heures qui menent a ce releve.
	q := "?" + url.Values{"heures": {"24"}, "fin": {snap.Timestamp.Local().Format(finLayout)}}.Encode()
	ds, us := byChannel(h.Downstream), byChannel(h.Upstream)
	var down, up []channelRow
	for _, c := range snap.Downstream {
		row := channelRow{ID: c.ChannelID, Modulation: c.Modulation, Power: c.PowerDBmV, SNR: c.SNRDB, Freq: c.FrequencyMHz,
			Href: "/descendant/" + strconv.Itoa(c.ChannelID) + q, Wide: strings.EqualFold(c.Modulation, "OFDM"), SNRColumn: true}
		row.Level, row.Note = levelNote(ds, c.ChannelID)
		down = append(down, row)
	}
	for _, c := range snap.Upstream {
		row := channelRow{ID: c.ChannelID, Modulation: c.Modulation, Power: c.PowerDBmV, Freq: c.FrequencyMHz,
			Href: "/montant/" + strconv.Itoa(c.ChannelID) + q, Wide: strings.EqualFold(c.Modulation, "OFDMA")}
		row.Level, row.Note = levelNote(us, c.ChannelID)
		up = append(up, row)
	}
	v.DownGroups = groupChannels(down, "OFDM")
	v.UpGroups = groupChannels(up, "OFDMA")
	s.render(w, http.StatusOK, "releve", v)
}

// findingsOf liste les mesures hors plage d'un releve, sans les dater : la
// page d'un releve passe montre l'instant, pas sa suite.
func findingsOf(h analyze.Health) []reason {
	var out []reason
	for _, side := range []struct {
		dir  string
		list []analyze.ChannelHealth
	}{{"descendant", h.Downstream}, {"montant", h.Upstream}} {
		for _, ch := range side.list {
			for _, f := range ch.Findings {
				if f.Level >= analyze.Warning {
					out = append(out, reason{Level: f.Level, Metric: f.Metric, Message: f.Message, Limit: f.Limit,
						Headline: ucFirst(f.Metric) + " : " + f.Message,
						Where:    channelWhere(side.dir, ch)})
				}
			}
		}
	}
	return out
}
