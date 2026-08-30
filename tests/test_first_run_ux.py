"""Template and localization contracts for the Mire first-run setup UX.

Mire has no demo mode and no Windows Desktop Preview: the first run goes
straight to connecting the modem, with a restore-from-backup escape hatch.
"""

import json
import re
from pathlib import Path

from app.i18n import LANGUAGES


ROOT = Path(__file__).resolve().parents[1]
I18N_DIR = ROOT / "app" / "i18n"
SETUP_TEMPLATE = ROOT / "app" / "templates" / "setup.html"
SETUP_SCRIPT = ROOT / "app" / "static" / "js" / "setup.js"

FIRST_RUN_KEYS = {
    "setup_value_title",
    "setup_value_desc",
    "setup_connect_modem",
    "setup_restore_action",
    "setup_try_again",
}


def test_setup_template_offers_connect_and_restore_paths():
    template = SETUP_TEMPLATE.read_text(encoding="utf-8")
    script = SETUP_SCRIPT.read_text(encoding="utf-8")

    assert 'class="first-run-card glass"' in template
    assert 'id="connect-modem-btn"' in template
    assert 'id="restore-action"' in template
    assert template.index("setup_connect_modem") < template.index("setup_restore_action")
    assert "showSetupRecovery" in script
    assert "retry.focus({preventScroll: true})" in script
    assert "function startFreshSetup()" in script
    assert "nextStep(1);" in script


def test_setup_surface_keeps_no_demo_or_desktop_preview_affordances():
    template = SETUP_TEMPLATE.read_text(encoding="utf-8")
    script = SETUP_SCRIPT.read_text(encoding="utf-8")

    for forbidden in ("start-demo-btn", "startDemo", "demo_mode", "desktop_preview"):
        assert forbidden not in template, forbidden
        assert forbidden not in script, forbidden


def test_first_run_keys_are_complete_nonempty_and_placeholder_compatible():
    locale_paths = sorted(I18N_DIR.glob("*.json"))
    assert {path.stem for path in locale_paths} == set(LANGUAGES)
    english = json.loads((I18N_DIR / "en.json").read_text(encoding="utf-8"))
    placeholder = re.compile(r"\{[^{}]+\}")
    problems = {}

    for path in locale_paths:
        data = json.loads(path.read_text(encoding="utf-8-sig"))
        locale_problems = []
        for key in FIRST_RUN_KEYS:
            value = data.get(key)
            if not isinstance(value, str) or not value.strip():
                locale_problems.append(f"{key}: missing")
                continue
            if set(placeholder.findall(value)) != set(
                placeholder.findall(english[key])
            ):
                locale_problems.append(f"{key}: placeholder mismatch")
        if locale_problems:
            problems[path.name] = locale_problems

    assert problems == {}
