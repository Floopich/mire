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

import math
from dataclasses import dataclass

# Seuil d'ouverture du droit : interruption TOTALE et continue.
# L'IBPT a mis en consultation une revision de l'article 113/2 portant ce
# seuil a 12 heures, sur le modele neerlandais. Proposition, pas droit
# positif : le seuil applicable reste 8 heures.
THRESHOLD_SECONDS = 8 * 3600

# Bareme d'entree en vigueur au 1er novembre 2024 : 1 EUR a 8 h, 1 EUR de
# plus a 24 h, puis 0,50 EUR par periode de 24 h supplementaire.
#
# Aucune indexation n'est prevue : ni la loi, ni la page de l'IBPT, ni
# l'expose des motifs du projet de revision n'en mentionnent une. Le
# coefficient reste disponible en option pour l'utilisateur qui aurait une
# source contraire, mais il n'est pas applique par defaut.
SCALE_REFERENCE_YEAR = 2024
BASE_EUR = 1.00
FIRST_DAY_EUR = 2.00
PER_EXTRA_DAY_EUR = 0.50

# Plancher : un trentieme de la redevance mensuelle PAR PERIODE de 24 h
# entamee, si le total depasse le bareme. La logique est explicitee dans
# l'expose des motifs du projet de revision : un abonne ne doit pas payer de
# redevance pour les jours ou il ne beneficie pas du service. C'est aussi la
# methode que publie Telenet. amount_min_eur conserve la lecture litterale de
# la synthese IBPT (un seul trentieme), comme borne basse conservatrice.
MONTHLY_FRACTION = 30

# Causes excluant toute compensation. force_majeure et le fait du client
# figurent dans la loi ; les deux autres sont les motifs que les operateurs
# opposent en pratique. customer_equipment vise ce qui n'appartient pas au
# reseau public : modem, decodeur, carte SIM, repeteurs -- l'exclusion la
# plus probable pour un outil branche sur le modem de l'abonne.
EXCLUSION_REASONS = (
    "force_majeure",
    "customer_action",
    "customer_equipment",
    "alternative_accepted",
)

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
    indexed: bool = False
    index_factor: float | None = None
    reference_year: int = SCALE_REFERENCE_YEAR
    amount_min_eur: float = 0.0
    floor_prorated_eur: float = 0.0
    floor_flat_eur: float = 0.0
    periods: int = 0

    def as_dict(self) -> dict:
        return {
            "eligible": self.eligible,
            "amount_eur": round(self.amount_eur, 2),
            "scale_amount_eur": round(self.scale_amount_eur, 2),
            "floor_applied": self.floor_applied,
            "counted_seconds": round(self.counted_seconds, 1),
            "reason": self.reason,
            "indexed": self.indexed,
            "index_factor": self.index_factor,
            "reference_year": self.reference_year,
            "amount_min_eur": round(self.amount_min_eur, 2),
            "floor_prorated_eur": round(self.floor_prorated_eur, 2),
            "floor_flat_eur": round(self.floor_flat_eur, 2),
            "periods_started": self.periods,
        }


def periods_started(counted_seconds: float) -> int:
    """Nombre de periodes de 24 h entamees.

    Le regulateur ecrit que toute periode de 24 heures entamee implique le
    payement de l indemnite pour la periode complete. On arrondit donc vers
    le haut, et non vers le bas comme le ferait un compte de jours revolus.
    """
    if counted_seconds <= 0:
        return 0
    return int(math.ceil(counted_seconds / SECONDS_PER_DAY))


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
    exclusion: str | None = None,
    index_factor: float | None = None,
) -> Compensation:
    """Calcule l'indemnite due pour une interruption deja qualifiee.

    counted_seconds est la duree LEGALE, qui court a partir du moment ou
    l'operateur a connaissance de l'interruption -- pas de l'instant ou la
    ligne est tombee. L'appelant doit fournir la duree corrigee.
    """
    if force_majeure and exclusion is None:
        exclusion = "force_majeure"
    if exclusion is not None:
        if exclusion not in EXCLUSION_REASONS:
            raise ValueError(f"unknown exclusion: {exclusion}")
        return Compensation(False, 0.0, 0.0, False, counted_seconds, exclusion)
    if counted_seconds < THRESHOLD_SECONDS:
        return Compensation(False, 0.0, 0.0, False, counted_seconds, "below_threshold")

    # L'indexation ne porte que sur le bareme. Le plancher se calcule sur la
    # redevance reelle, qui est deja au tarif courant.
    scale = scale_amount(counted_seconds)
    indexed = index_factor is not None and index_factor > 0
    if indexed:
        scale = scale * index_factor
    periods = periods_started(counted_seconds)
    floor_flat = 0.0
    floor_prorated = 0.0
    if monthly_fee_eur and monthly_fee_eur > 0:
        floor_flat = monthly_fee_eur / MONTHLY_FRACTION
        floor_prorated = floor_flat * periods
    amount = max(scale, floor_prorated)
    return Compensation(
        eligible=True,
        amount_eur=amount,
        scale_amount_eur=scale,
        floor_applied=floor_prorated > scale,
        counted_seconds=counted_seconds,
        reason="eligible",
        indexed=indexed,
        index_factor=index_factor if indexed else None,
        amount_min_eur=max(scale, floor_flat),
        floor_prorated_eur=floor_prorated,
        floor_flat_eur=floor_flat,
        periods=periods,
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
