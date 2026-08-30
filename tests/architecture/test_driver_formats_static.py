"""Static guards for the driver and format surface Mire actually ships.

Mire ships exactly two drivers:

* ``generic``      — no DOCSIS data, delegates to the ``generic_no_docsis`` profile.
* ``voo_cga4233``  — Technicolor CGA4233 on VOO firmware, delegating to the
  ``voo_cga4233_json`` profile.

The upstream fleet contract (21 driver classes, 23 format profiles) was removed
with the drivers it described.
"""

from __future__ import annotations

import ast
import importlib
from pathlib import Path

from app.drivers import driver_registry
from app.drivers.formats import FORMAT_PROFILE_MODULES


ROOT = Path(__file__).resolve().parents[2]
FORMATS = ROOT / "app" / "drivers" / "formats"

# Format profiles stay pure: no transport, auth, randomness, or wall-clock.
FORBIDDEN_IMPORTS = {
    "cryptography", "flask", "random", "requests", "secrets", "socket", "ssl", "time",
}

EXPECTED_REGISTRY_KEYS = {"generic", "voo_cga4233"}

PROFILE_ENTRYPOINTS = {
    "generic_no_docsis": "parse_generic_no_docsis",
    "voo_cga4233_json": "parse_voo_cga4233_json",
}

# Modules that stay in the formats package because a shipped driver uses them.
EXPECTED_FORMAT_MODULES = {"__init__", "boundaries", "contract", "primitives", "voo"}


def test_registry_exposes_exactly_the_shipped_drivers():
    assert {key for key, _label in driver_registry.get_available_drivers()} == EXPECTED_REGISTRY_KEYS


def test_formats_package_contains_no_orphan_parser_modules():
    present = {path.stem for path in FORMATS.glob("*.py")}
    assert present == EXPECTED_FORMAT_MODULES


def test_formats_modules_have_no_transport_auth_or_runtime_dependencies():
    issues = []
    for path in sorted(FORMATS.glob("*.py")):
        tree = ast.parse(path.read_text(encoding="utf-8"), filename=str(path))
        for node in ast.walk(tree):
            imported = []
            if isinstance(node, ast.Import):
                imported = [alias.name for alias in node.names]
            elif isinstance(node, ast.ImportFrom) and node.module:
                imported = [node.module]
            for name in imported:
                if name.split(".", 1)[0] in FORBIDDEN_IMPORTS:
                    issues.append(f"{path.name}:{node.lineno} imports {name}")
    assert issues == []


def test_every_registered_profile_has_one_named_entrypoint():
    assert set(PROFILE_ENTRYPOINTS) == set(FORMAT_PROFILE_MODULES)
    for profile, function_name in PROFILE_ENTRYPOINTS.items():
        module = importlib.import_module(FORMAT_PROFILE_MODULES[profile])
        assert callable(getattr(module, function_name, None)), (profile, function_name)


def test_every_driver_delegates_to_a_registered_profile():
    from app.drivers.generic import GenericDriver
    from app.drivers.voo_cga4233 import VooCGA4233Driver

    assert GenericDriver.FORMAT_FAMILIES == ("generic_no_docsis",)
    assert VooCGA4233Driver.FORMAT_FAMILIES == ("voo_cga4233_json",)
    for driver_cls in (GenericDriver, VooCGA4233Driver):
        assert set(driver_cls.FORMAT_FAMILIES) <= set(FORMAT_PROFILE_MODULES)
