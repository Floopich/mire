// Package compensation calcule la compensation legale due en Belgique pour
// une interruption de service.
//
// Source : article 113/2 de la loi du 13 juin 2005 relative aux
// communications electroniques, en vigueur depuis le 1er novembre 2024.
// Bareme publie par l'IBPT, sans indexation. Seule l'interruption totale et
// continue ouvre ce droit. Le calcul est une estimation fondee sur la duree
// mesuree.
package compensation

import (
	"math"
	"time"
)

const (
	// Threshold est la duree a partir de laquelle la compensation est due.
	Threshold = 8 * time.Hour
	day       = 24 * time.Hour

	// Bareme : 1 euro a 8 heures, 1 euro de plus a 24 heures, puis 0,50 euro
	// par jour supplementaire.
	baseEUR     = 1.00
	firstDayEUR = 2.00
	extraDayEUR = 0.50
	// monthlyFraction : la compensation vaut au moins un trentieme de la
	// redevance mensuelle par periode de 24 heures entamee, si c'est plus
	// que le bareme. Pour une offre groupee, c'est le prix du pack.
	monthlyFraction = 30
)

// Result est la compensation estimee pour une interruption.
type Result struct {
	Eligible bool
	// Amount est le montant du : le bareme, ou le plancher s'il est plus eleve.
	Amount float64
	Scale  float64
	Floor  float64
	// FloorApplied dit que le plancher l'emporte sur le bareme.
	FloorApplied bool
	// Periods compte les periodes de 24 heures entamees.
	Periods int
}

// Compute estime la compensation d'une interruption de duree d. monthlyFee
// est la redevance mensuelle en euros, 0 si elle n'est pas renseignee.
func Compute(d time.Duration, monthlyFee float64) Result {
	if d < Threshold {
		return Result{}
	}
	r := Result{Eligible: true, Scale: scale(d), Periods: int(math.Ceil(float64(d) / float64(day)))}
	if monthlyFee > 0 {
		r.Floor = monthlyFee / monthlyFraction * float64(r.Periods)
	}
	r.Amount = r.Scale
	if r.Floor > r.Scale {
		r.Amount, r.FloorApplied = r.Floor, true
	}
	r.Amount = math.Round(r.Amount*100) / 100
	return r
}

func scale(d time.Duration) float64 {
	if d < day {
		return baseEUR
	}
	fullDays := int(d / day)
	return firstDayEUR + extraDayEUR*float64(fullDays-1)
}
