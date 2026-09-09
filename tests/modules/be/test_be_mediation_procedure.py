"""Recevabilite de la plainte : motifs de blocage connus."""

from app.modules.be_mediation import procedure


def test_written_complaint_with_prior_contact_has_no_blocker():
    result = procedure.assess({"complaint_is_written": True})
    assert result["admissible"] is True
    assert result["blockers"] == []


def test_missing_written_form_blocks():
    result = procedure.assess({"complaint_is_written": False})
    assert result["admissible"] is False
    assert "not_written" in result["blockers"]


def test_each_known_blocker_is_reported():
    for blocker in procedure.BLOCKERS:
        result = procedure.assess({"complaint_is_written": True, blocker: True})
        assert result["admissible"] is False
        assert blocker in result["blockers"]


def test_handling_and_age_limits_are_exposed():
    result = procedure.assess({"complaint_is_written": True})
    assert result["handling_days"] == 90
    assert result["max_age_days"] == 365


def test_steps_start_with_the_operator():
    assert procedure.STEPS[0] == "contact_operator"
