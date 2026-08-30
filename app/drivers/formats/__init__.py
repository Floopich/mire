"""Pure, explicit modem payload format profiles."""

from __future__ import annotations

from types import MappingProxyType

from .contract import ParseDiagnostic, ParseResult


FORMAT_PROFILE_MODULES = MappingProxyType({
    "generic_no_docsis": "app.drivers.formats.boundaries",
    "voo_cga4233_json": "app.drivers.formats.voo",
})

__all__ = ["FORMAT_PROFILE_MODULES", "ParseDiagnostic", "ParseResult"]
