"""Focused behavior and migration checks for the browser URL contract."""

from __future__ import annotations

import json
from pathlib import Path
import re
import subprocess

import pytest


ROOT = Path(__file__).resolve().parents[1]
HELPER = ROOT / "app/static/js/url-contract.js"

EXPECTED_CONTRACT_CALLS = {
    "app/static/js/dashboard.js": 1,
    "app/static/js/service-worker-registration.js": 3,
    "app/static/js/setup.js": 7,
    "app/static/js/channels.js": 7,
    "app/static/js/correlation.js": 4,
    "app/static/js/events.js": 5,
    "app/static/js/glossary.js": 1,
    "app/static/js/hero-chart.js": 1,
    "app/static/js/journal.js": 22,
    "app/static/js/notices.js": 1,
    "app/static/js/settings.js": 23,
    "app/static/js/sparklines.js": 1,
    "app/static/js/speedtest.js": 6,
    "app/static/js/trends.js": 2,
    "app/static/js/utils.js": 3,
    "app/modules/comparison/static/main.js": 1,
    "app/modules/evidence/static/main.js": 2,
    "app/modules/connection_monitor/static/js/connection-monitor-card.js": 1,
    "app/modules/connection_monitor/static/js/connection-monitor-detail.js": 13,
    "app/modules/connection_monitor/static/js/connection-monitor-settings.js": 4,
    "app/modules/modulation/static/main.js": 2,
}

NODE_HARNESS = r"""
const fs = require('fs');
const request = JSON.parse(fs.readFileSync(0, 'utf8'));
global.window = {};
global.document = {
    getElementById: function(id) {
        if (id !== 'mire-url-bootstrap' || !request.hasElement) return null;
        return {textContent: request.bootstrapText};
    }
};
let initError = null;
try {
    eval(fs.readFileSync(process.argv[1], 'utf8'));
} catch (error) {
    initError = error.name + ': ' + error.message;
}
const results = request.inputs.map(function(value) {
    try {
        return {ok: true, value: window.mireUrl(value)};
    } catch (error) {
        return {ok: false, error: error.name + ': ' + error.message};
    }
});
process.stdout.write(JSON.stringify({initError: initError, results: results}));
"""

DEMO_REDIRECT_HARNESS = r"""
const fs = require('fs');
const request = JSON.parse(fs.readFileSync(0, 'utf8'));
const button = {disabled: false};
const banner = {querySelectorAll: function() { return [button]; }};
const result = {textContent: ''};
const assignments = [];
global.window = {
    T: {},
    mireConfirm: async function() { return true; },
    location: {assign: function(value) { assignments.push(value); }}
};
global.document = {
    getElementById: function(id) {
        if (id === 'demo-banner') return banner;
        if (id === 'demo-banner-result') return result;
        return null;
    }
};
global.mireUrl = function(value) {
    if (typeof value !== 'string' || value.charAt(0) !== '/' || value.charAt(1) === '/') {
        throw new TypeError('unsafe URL');
    }
    return '/mire' + value;
};
global.fetch = async function() {
    return {
        status: 200,
        ok: true,
        json: async function() { return {success: true, next: request.responseNext}; }
    };
};
eval(fs.readFileSync(process.argv[1], 'utf8'));
window.leaveDemo(request.nextChoice, button).then(function() {
    process.stdout.write(JSON.stringify({assignments: assignments, disabled: button.disabled}));
});
"""


def _run_helper(
    bootstrap: object = None,
    inputs: list[object] | None = None,
    *,
    bootstrap_text: str | None = None,
    has_element: bool = True,
) -> dict[str, object]:
    if bootstrap_text is None:
        bootstrap_text = json.dumps(
            {"basePath": ""} if bootstrap is None else bootstrap,
            separators=(",", ":"),
        )
    completed = subprocess.run(
        ["node", "-e", NODE_HARNESS, str(HELPER)],
        input=json.dumps(
            {
                "hasElement": has_element,
                "bootstrapText": bootstrap_text,
                "inputs": inputs or [],
            }
        ),
        text=True,
        capture_output=True,
        check=True,
    )
    return json.loads(completed.stdout)


def _run_demo_redirect(next_choice: str, response_next: str) -> dict[str, object]:
    completed = subprocess.run(
        ["node", "-e", DEMO_REDIRECT_HARNESS, str(DEMO_BANNER)],
        input=json.dumps({"nextChoice": next_choice, "responseNext": response_next}),
        text=True,
        capture_output=True,
        check=True,
    )
    return json.loads(completed.stdout)


@pytest.mark.parametrize(
    ("base_path", "source", "expected"),
    [
        ("", "/api/poll", "/api/poll"),
        ("", "/api/export?q=a%20b#part", "/api/export?q=a%20b#part"),
        ("/mire", "/", "/mire/"),
        ("/mire", "/api/poll", "/mire/api/poll"),
        (
            "/mire",
            "/api/export?q=a%20b#part%2Fkept",
            "/mire/api/export?q=a%20b#part%2Fkept",
        ),
        ("/mire", "/mire", "/mire"),
        ("/mire", "/mire/api/poll", "/mire/api/poll"),
        ("/mire", "/mire-extra/api", "/mire/mire-extra/api"),
        ("/mire", "/file%20name", "/mire/file%20name"),
        ("/A.z_~-/b", "/api/poll", "/A.z_~-/b/api/poll"),
    ],
)
def test_mire_url_root_and_prefixed_behavior(base_path, source, expected):
    outcome = _run_helper({"basePath": base_path}, [source])

    assert outcome["initError"] is None
    assert outcome["results"] == [{"ok": True, "value": expected}]


@pytest.mark.parametrize(
    "source",
    [
        None,
        1,
        {},
        "",
        "api/poll",
        "?lang=en",
        "#events",
        "http://evil.example/",
        "https://evil.example/",
        "javascript:alert(1)",
        "data:text/plain,hello",
        "blob:https://example.test/id",
        "//evil.example/path",
        "///evil.example/path",
        "/api\\poll",
        "/api\x00poll",
        "/api\x1fpoll",
        "/api\x7fpoll",
        "/api/%",
        "/api/%2",
        "/api/%GG",
        "/api?value=%",
        "/api#value=%GG",
        "/api/./poll",
        "/api/../poll",
        "/api/%2e/poll",
        "/api/%2E%2e/poll",
        "/api%2fpoll",
        "/api%2Fpoll",
        "/api%5cpoll",
        "/api%00poll",
        "/api%1fpoll",
        "/api%7fpoll",
        "/api/%252e%252e/poll",
        "/api%252fpoll",
        "/api%255cpoll",
        "/api%2500poll",
        "/api/%25252e%25252e/poll",
        "/api/%25GG",
    ],
)
def test_mire_url_rejects_unsafe_or_ambiguous_inputs(source):
    outcome = _run_helper({"basePath": "/mire"}, [source])

    assert outcome["initError"] is None
    assert outcome["results"][0]["ok"] is False


@pytest.mark.parametrize(
    ("bootstrap", "bootstrap_text", "has_element"),
    [
        ({}, None, True),
        ({"basePath": "", "token": "secret"}, None, True),
        ({"basePath": None}, None, True),
        ({"basePath": "/"}, None, True),
        ({"basePath": "/mire/"}, None, True),
        ({"basePath": "mire"}, None, True),
        ({"basePath": "//mire"}, None, True),
        ({"basePath": "/doc%73ight"}, None, True),
        ({"basePath": "/mire?x"}, None, True),
        (None, "not json", True),
        (None, "null", True),
        (None, None, False),
    ],
)
def test_invalid_or_missing_bootstrap_fails_closed(
    bootstrap, bootstrap_text, has_element
):
    outcome = _run_helper(
        bootstrap,
        ["/api/poll"],
        bootstrap_text=bootstrap_text,
        has_element=has_element,
    )

    assert outcome["initError"] is not None
    assert outcome["results"][0]["ok"] is False


def test_helper_does_not_patch_browser_primitives():
    source = HELPER.read_text(encoding="utf-8")

    assert "Object.defineProperty(window, 'mireUrl'" in source
    assert "configurable: false" in source
    assert "writable: false" in source
    assert "window.mireUrl =" not in source
    assert "window.fetch" not in source
    assert "XMLHttpRequest" not in source
    assert re.search(
        r"\b(?:Document|Element|Location|Node|Window)\.prototype\b", source
    ) is None
    assert "window.location" not in source


def test_bootstrap_base_path_is_rebuilt_from_encoded_validated_segments():
    source = HELPER.read_text(encoding="utf-8")

    assert "canonicalSegments.push(encodeURIComponent(segments[" in source
    assert "basePath = '/' + canonicalSegments.join('/')" in source


REPRESENTATIVE_SITES = [
    (
        "direct fetch",
        ROOT / "app/static/js/speedtest.js",
        "fetch(mireUrl('/api/speedtest?count=2000'))",
    ),
    (
        "assigned URL then fetch",
        ROOT / "app/static/js/channels.js",
        "var url = mireUrl('/api/weather/range?start='",
    ),
    (
        "image source",
        ROOT / "app/modules/modulation/static/main.js",
        "var url = mireUrl('/api/modulation/intraday?direction=' + _modDirection);",
    ),
    (
        "download href",
        ROOT / "app/static/js/events.js",
        "exportLink.href = mireUrl('/api/events/export.csv'",
    ),
    (
        "navigation",
        ROOT / "app/static/js/utils.js",
        "window.location.href = mireUrl('/api/report?'",
    ),
    (
        "built-in module",
        ROOT / "app/modules/comparison/static/main.js",
        "var url = mireUrl('/api/comparison?from_a='",
    ),
    (
        "connection-monitor settings asset",
        ROOT / "app/modules/connection_monitor/static/js/connection-monitor-settings.js",
        "fetch(mireUrl('/api/connection-monitor/targets/' + target.id)",
    ),
]


def _missing_representative_sites(overrides: dict[Path, str] | None = None) -> list[str]:
    overrides = overrides or {}
    missing = []
    for label, path, required in REPRESENTATIVE_SITES:
        source = overrides.get(path, path.read_text(encoding="utf-8"))
        if required not in source:
            missing.append(label)
    return missing


def test_representative_real_url_sites_use_the_contract():
    assert _missing_representative_sites() == []


def test_inventoried_files_keep_the_reviewed_contract_sites():
    actual = {
        relative: (ROOT / relative).read_text(encoding="utf-8").count("mireUrl(")
        for relative in EXPECTED_CONTRACT_CALLS
    }

    assert actual == EXPECTED_CONTRACT_CALLS
    assert sum(actual.values()) == 110  # reviewed browser URL contract sites


def test_inventoried_actual_literal_forms_have_no_unwrapped_url_sink():
    offenders = []
    forbidden_forms = (
        "fetch('/api",
        'fetch("/api',
        "fetch('/health",
        'fetch("/health',
        "var url = '/api",
        'var url = "/api',
        "return '/api",
        'return "/api',
        'href="/api',
        "href='/api",
        'src="/api',
        "src='/api",
        "window.location.assign('/login')",
        'window.location.assign("/login")',
        "window.location.href = '/api",
        'window.location.href = "/api',
    )
    for relative in EXPECTED_CONTRACT_CALLS:
        source = (ROOT / relative).read_text(encoding="utf-8")
        for form in forbidden_forms:
            if form in source:
                offenders.append(f"{relative}: {form}")

    assert offenders == []


@pytest.mark.parametrize("label,path,required", REPRESENTATIVE_SITES)
def test_representative_site_mutations_are_detected(label, path, required):
    source = path.read_text(encoding="utf-8")
    mutated = source.replace(required, required.replace("mireUrl(", "", 1), 1)

    assert mutated != source
    assert label in _missing_representative_sites({path: mutated})


def test_pwa_fetches_use_the_browser_url_contract():
    settings = (ROOT / "app/static/js/settings.js").read_text(encoding="utf-8")
    unwrapped = [
        line.strip()
        for line in settings.splitlines()
        if "fetch('/" in line
    ]

    assert unwrapped == []
    assert "fetch(mireUrl('/api/notifications/pwa/status'))" in settings
    assert "fetch(mireUrl('/api/notifications/pwa/subscribe')," in settings
    assert "fetch(mireUrl('/api/notifications/pwa/unsubscribe')," in settings
