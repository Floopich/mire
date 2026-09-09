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


def test_monthly_floor_beats_the_scale_when_higher():
    result = rules.compute(8 * H, monthly_fee_eur=60)
    assert result.scale_amount_eur == pytest.approx(1.0)
    assert result.amount_eur == pytest.approx(2.0)
    assert result.floor_applied is True


def test_scale_wins_when_floor_is_lower():
    result = rules.compute(10 * 24 * H, monthly_fee_eur=15)
    assert result.floor_applied is False
    assert result.amount_eur == pytest.approx(rules.scale_amount(10 * 24 * H))


def test_counted_duration_starts_at_operator_awareness():
    detected, reported, end = 1000.0, 4600.0, 40000.0
    assert rules.counted_duration(detected, end, reported, end) == pytest.approx(
        end - reported
    )


def test_counted_duration_ignores_a_report_made_before_the_outage():
    assert rules.counted_duration(1000.0, 5000.0, 500.0, 5000.0) == pytest.approx(4000.0)


def test_ongoing_outage_is_measured_up_to_now():
    assert rules.counted_duration(1000.0, None, None, 30000.0) == pytest.approx(29000.0)
