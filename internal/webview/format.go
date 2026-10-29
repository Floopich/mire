package webview

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/floopich/mire/internal/analyze"
)

// Les nombres et les heures sont rendus a la francaise : virgule decimale,
// espace insecable avant l'unite, date jour/mois.
const nbsp = " "

// Fmt rend une valeur optionnelle, en distinguant l'absence du zero.
func Fmt(v *float64, unit string) string {
	if v == nil {
		return "—"
	}
	return dec(*v) + nbsp + unit
}

// dec rend un nombre a une decimale, avec une virgule et un vrai signe moins.
func dec(v float64) string {
	s := strings.Replace(strconv.FormatFloat(v, 'f', 1, 64), ".", ",", 1)
	if s == "-0,0" {
		s = "0,0"
	}
	return strings.Replace(s, "-", "\u2212", 1)
}

// FmtMHz rend une frequence au dixieme de MHz, sans decimale quand elle est
// entiere : 442 MHz, 37,2 MHz. Les canaux montants SC-QAM sont rarement
// centres sur un MHz rond.
func FmtMHz(v *float64) string {
	if v == nil {
		return "—"
	}
	r := math.Round(*v*10) / 10
	if r == math.Trunc(r) {
		return strconv.Itoa(int(r)) + nbsp + "MHz"
	}
	return dec(r) + nbsp + "MHz"
}

// FmtTime rend un horodatage en heure locale.
func FmtTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Local().Format("02/01/2006 à 15:04")
}

// FmtCount rend un entier avec separateur de milliers : 12 480.
func FmtCount(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// Ago rend un delai en langage courant : « il y a 4 min ».
func Ago(t, now time.Time) string {
	if t.IsZero() {
		return "jamais"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "à l'instant"
	case d < time.Hour:
		return fmt.Sprintf("il y a %d%smin", int(d.Minutes()), nbsp)
	case d < 48*time.Hour:
		return fmt.Sprintf("il y a %d%sh", int(d.Hours()), nbsp)
	default:
		return fmt.Sprintf("il y a %d%sjours", int(d.Hours()/24), nbsp)
	}
}

// Label rend un niveau dans les mots de l'interface.
//
// analyze.Level.String reste en ASCII : il sert de classe CSS et de valeur
// d'API, et ne doit pas changer quand on reformule un libelle.
func Label(l analyze.Level) string {
	switch l {
	case analyze.Good:
		return "Bon"
	case analyze.Tolerated:
		return "Toléré"
	case analyze.Warning:
		return "À surveiller"
	case analyze.Critical:
		return "Critique"
	default:
		return "Non qualifié"
	}
}

func escape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	return r.Replace(s)
}
