"""Bareme legal belge : verrouille les seuils et les montants.

Ces valeurs viennent de l'article 113/2 LCE et du bareme IBPT. Un changement
silencieux ferait annoncer a l'utilisateur un montant qui n'est pas du.
"""

import pytest

from app.modules.be_compensation import rules

H = 3600


@pytest.mark.parametrize(
    "hours,expected",
    [
        (0, 0.0),
        (7.99, 0.0),
        (8, 1.0),
        (23.9, 1.0),
        (24, 2.0),
        (47.9, 2.0),
        (48, 2.5),
        (72, 3.0),
        (168, 5.0),
    ],
)
def test_scale_matches_published_amounts(hours, expected):
    assert rules.scale_amount(hours * H) == pytest.approx(expected)


def test_below_threshold_is_not_eligible():
    result = rules.compute(7.5 * H)
    assert result.eligible is False
    assert result.amount_eur == 0.0
    assert result.reason == "below_threshold"


def test_force_majeure_blocks_even_a_long_outage():
    result = rules.compute(10 * 24 * H, monthly_fee_eur=60, force_majeure=True)
    assert result.eligible is False
    assert result.amount_eur == 0.0
    assert result.reason == "force_majeure"


@pytest.mark.parametrize("cause", rules.EXCLUSION_REASONS)
def test_every_exclusion_blocks_compensation(cause):
    result = rules.compute(10 * 24 * H, monthly_fee_eur=60, exclusion=cause)
    assert result.eligible is False
    assert result.amount_eur == 0.0
    assert result.reason == cause


def test_unknown_exclusion_is_rejected():
    with pytest.raises(ValueError):
        rules.compute(10 * 24 * H, exclusion="pas-un-motif")


def test_amounts_are_flagged_as_not_indexed_by_default():
    """Aucune indexation n'est sourcee : le coefficient reste optionnel et
    les montants publies s'appliquent tels quels par defaut."""
    result = rules.compute(48 * H)
    assert result.indexed is False
    assert result.index_factor is None
    assert result.reference_year == rules.SCALE_REFERENCE_YEAR
    assert result.amount_eur == pytest.approx(2.50)


def test_index_factor_scales_the_published_amounts():
    result = rules.compute(48 * H, index_factor=1.08)
    assert result.indexed is True
    assert result.index_factor == pytest.approx(1.08)
    assert result.amount_eur == pytest.approx(2.70)


def test_index_factor_does_not_touch_the_monthly_floor():
    """La redevance saisie est deja au tarif courant : l'indexer serait
    l'appliquer deux fois."""
    result = rules.compute(8 * H, monthly_fee_eur=60, index_factor=1.08)
    assert result.floor_applied is True
    assert result.amount_eur == pytest.approx(2.00)


def test_monthly_floor_beats_the_scale_when_higher():
    result = rules.compute(8 * H, monthly_fee_eur=60)
    assert result.scale_amount_eur == pytest.approx(1.0)
    assert result.amount_eur == pytest.approx(2.0)
    assert result.floor_applied is True


def test_scale_wins_when_floor_is_lower():
    result = rules.compute(10 * 24 * H, monthly_fee_eur=15)
    assert result.floor_applied is False
    assert result.amount_eur == pytest.approx(rules.scale_amount(10 * 24 * H))


def test_floor_is_counted_per_started_24h_period():
    """Sept jours a 60 EUR/mois : 2 EUR par periode, pas 2 EUR au total."""
    result = rules.compute(7 * 24 * H, monthly_fee_eur=60)
    assert result.periods == 7
    assert result.floor_prorated_eur == pytest.approx(14.0)
    assert result.amount_eur == pytest.approx(14.0)
    assert result.floor_applied is True


def test_started_period_counts_as_a_whole_one():
    """Une interruption de 25 h a entame une deuxieme periode de 24 h."""
    assert rules.periods_started(25 * H) == 2
    assert rules.periods_started(24 * H) == 1
    assert rules.periods_started(0.5 * H) == 1
    assert rules.periods_started(0) == 0


def test_conservative_bound_keeps_the_literal_ibpt_reading():
    """amount_min_eur garde le trentieme unique, sans proratisation."""
    result = rules.compute(7 * 24 * H, monthly_fee_eur=60)
    assert result.floor_flat_eur == pytest.approx(2.0)
    assert result.amount_min_eur == pytest.approx(rules.scale_amount(7 * 24 * H))
    assert result.amount_min_eur < result.amount_eur


def test_counted_duration_starts_at_operator_awareness():
    detected, reported, end = 1000.0, 4600.0, 40000.0
    assert rules.counted_duration(detected, end, reported, end) == pytest.approx(
        end - reported
    )


def test_counted_duration_ignores_a_report_made_before_the_outage():
    assert rules.counted_duration(1000.0, 5000.0, 500.0, 5000.0) == pytest.approx(4000.0)


def test_ongoing_outage_is_measured_up_to_now():
    assert rules.counted_duration(1000.0, None, None, 30000.0) == pytest.approx(29000.0)
