"""Durable static contracts for API-backed DOM rendering."""

from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


def test_smokeping_cards_and_error_fallback_use_dom_nodes():
    source = (ROOT / "app/modules/smokeping/static/main.js").read_text(
        encoding="utf-8"
    )

    assert ".innerHTML" not in source
    assert "headerLabel.textContent = target" in source
    assert "fallback.textContent" in source
