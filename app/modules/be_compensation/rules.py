"""Bareme legal belge d'indemnisation pour interruption de service.

Source : article 113/2 de la loi du 13 juin 2005 relative aux communications
electroniques, en vigueur depuis le 1er novembre 2024. Bareme publie par
l'IBPT :
https://www.ibpt.be/consommateurs/telephonie-internet-tv/protection-des-consommateurs/compensation-pour-interruption-de-service

Ce module ne couvre QUE l'interruption totale. Une degradation de debit ou de
qualite n'ouvre aucun droit a cette compensation : c'est la difference de fond
avec le regime allemand (TKG) dont Mire est derive. Voir be_mediation pour la
voie applicable a une degradation.
"""

from __future__ import annotations

from dataclasses import dataclass

# Seuil d'ouverture du droit : interruption TOTALE et continue.
THRESHOLD_SECONDS = 8 * 3600

# Bareme : 1 EUR a 8 h, 1 EUR de plus a 24 h, puis 0,50 EUR par jour entame
# supplementaire.
BASE_EUR = 1.00
FIRST_DAY_EUR = 2.00
PER_EXTRA_DAY_EUR = 0.50

# Plancher : un trentieme de la redevance mensuelle, si superieur au bareme.
MONTHLY_FRACTION = 30

SECONDS_PER_DAY = 86400


@dataclass(frozen=True)
class Compensation:
    """Resultat du calcul pour une interruption."""

    eligible: bool
    amount_eur: float
    scale_amount_eur: float
    floor_applied: bool
    counted_seconds: float
    reason: str

    def as_dict(self) -> dict:
        return {
            "eligible": self.eligible,
            "amount_eur": round(self.amount_eur, 2),
            "scale_amount_eur": round(self.scale_amount_eur, 2),
            "floor_applied": self.floor_applied,
            "counted_seconds": round(self.counted_seconds, 1),
            "reason": self.reason,
        }


def scale_amount(counted_seconds: float) -> float:
    """Montant du bareme seul, hors plancher."""
    if counted_seconds < THRESHOLD_SECONDS:
        return 0.0
    if counted_seconds < SECONDS_PER_DAY:
        return BASE_EUR
    full_days = int(counted_seconds // SECONDS_PER_DAY)
    return FIRST_DAY_EUR + PER_EXTRA_DAY_EUR * (full_days - 1)


def compute(
    counted_seconds: float,
    monthly_fee_eur: float | None = None,
    force_majeure: bool = False,
) -> Compensation:
    """Calcule l'indemnite due pour une interruption deja qualifiee.

    counted_seconds est la duree LEGALE, qui court a partir du moment ou
    l'operateur a connaissance de l'interruption -- pas de l'instant ou la
    ligne est tombee. L'appelant doit fournir la duree corrigee.
    """
    if force_majeure:
        return Compensation(False, 0.0, 0.0, False, counted_seconds, "force_majeure")
    if counted_seconds < THRESHOLD_SECONDS:
        return Compensation(False, 0.0, 0.0, False, counted_seconds, "below_threshold")

    scale = scale_amount(counted_seconds)
    floor = 0.0
    if monthly_fee_eur and monthly_fee_eur > 0:
        floor = monthly_fee_eur / MONTHLY_FRACTION
    amount = max(scale, floor)
    return Compensation(
        eligible=True,
        amount_eur=amount,
        scale_amount_eur=scale,
        floor_applied=floor > scale,
        counted_seconds=counted_seconds,
        reason="eligible",
    )


def counted_duration(
    detected_start: float,
    detected_end: float | None,
    reported_at: float | None,
    now: float,
) -> float:
    """Duree retenue par la loi.

    Le compteur demarre a la connaissance de l'operateur : notification a
    l'IBPT, ou premier signalement d'un utilisateur. Sans horodatage de
    signalement, on ne peut pas prejuger de cette date -- on retourne alors la
    duree observee, que l'interface signale comme non qualifiee.
    """
    end = detected_end if detected_end is not None else now
    start = detected_start
    if reported_at is not None and reported_at > detected_start:
        start = reported_at
    return max(0.0, end - start)
