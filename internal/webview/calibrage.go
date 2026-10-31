package webview

import (
	"archive/zip"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"time"

	"github.com/floopich/mire/internal/store"
)

// Les seuils d'un operateur se valident sur des lignes reelles. L'archive de
// calibrage rassemble les releves d'une periode, sans rien qui designe
// l'abonne : ni adresse, ni numero de serie, ni reseau, ni secret, ni note.

// CalibrationIssueURL ouvre la demande qui recoit l'archive.
const CalibrationIssueURL = "https://github.com/floopich/mire/issues/new?template=calibrage.yml"

// calibrationTables sont les tableaux repris : les canaux, et ce que Mire en
// a conclu. Le debit (nom du serveur de mesure) et la sonde n'y sont pas.
var calibrationTables = []string{"descendant", "montant", "coupures", "journal"}

const calibrationReadme = `Archive de calibrage Mire

Relevés du modem sur la période, pour vérifier les seuils de l'opérateur.

Contenu :
- ligne.json : version de Mire, profil d'opérateur, modèle de modem, période ;
- descendant.csv, montant.csv : canaux, à chaque relevé ;
- coupures.csv : coupures vues par la sonde ;
- journal.csv : changements d'état et événements, sans vos notes.

Absent : adresse du modem, identifiant, mots de passe, jetons, numéro de
série, adresses MAC et IP, noms Wi-Fi, débit, notes du journal.
`

// calibrationInfo est le contenu de ligne.json.
type calibrationInfo struct {
	Mire       string `json:"mire"`
	Profil     string `json:"profil"`
	Modem      string `json:"modem"`
	DOCSIS     string `json:"docsis,omitempty"`
	Intervalle int    `json:"intervalle_s"`
	Du         string `json:"du"`
	Au         string `json:"au"`
}

// handleCalibration sert l'archive de calibrage de la periode demandee.
func (s *Server) handleCalibration(w http.ResponseWriter, r *http.Request) {
	from, to, msg := reportPeriod(r.URL.Query(), midnight(s.opts.Now()))
	if msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}
	st := s.opts.Service.State()
	info := calibrationInfo{Mire: s.opts.Version, Profil: st.Profile.ID, Modem: st.Model.ID,
		Intervalle: int(st.Interval / time.Second),
		Du:         from.Format(time.DateOnly), Au: to.AddDate(0, 0, -1).Format(time.DateOnly)}
	if snap, err := store.LatestSnapshot(r.Context(), s.db); err == nil {
		info.DOCSIS = snap.DOCSIS
	}

	file := "mire-calibrage-" + info.Du + "-" + info.Au + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+file+`"`)
	w.Header().Set("Cache-Control", "no-store")
	zw := zip.NewWriter(w)
	err := func() error {
		f, err := zw.Create("lisez-moi.txt")
		if err != nil {
			return err
		}
		if _, err := f.Write([]byte(calibrationReadme)); err != nil {
			return err
		}
		if f, err = zw.Create("ligne.json"); err != nil {
			return err
		}
		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")
		if err := enc.Encode(info); err != nil {
			return err
		}
		for _, name := range calibrationTables {
			e, _ := store.ExportByName(name)
			f, err := zw.Create(name + ".csv")
			if err != nil {
				return err
			}
			cw := csv.NewWriter(f)
			err = e.Write(r.Context(), s.db, from, to, func(row []string) error {
				// Les notes sont celles de l'abonne : elles restent chez lui.
				if name == "journal" && len(row) > 1 && row[1] == store.NoteKind {
					return nil
				}
				return cw.Write(row)
			})
			if cw.Flush(); err == nil {
				err = cw.Error()
			}
			if err != nil {
				return err
			}
		}
		return zw.Close()
	}()
	if err != nil {
		// L'en-tete est parti : l'archive sera tronquee, le journal dit pourquoi.
		s.opts.Logger.Error("archive de calibrage", "erreur", err)
	}
}
