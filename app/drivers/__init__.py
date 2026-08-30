"""Modem driver abstractions."""

from .registry import DriverRegistry

driver_registry = DriverRegistry()

driver_registry.register_builtin(
    "voo_cga4233",
    "app.drivers.voo_cga4233.VooCGA4233Driver",
    "voo_cga4233",
    # Identifiant du firmware VOO, confirme par un login reussi contre un
    # CGA4233VOO en bridge le 2026-08-30 ("admin" est rejete : MSG_LOGIN_1).
    hints={"default_url": "http://192.168.100.1", "default_user": "voo"},
)
driver_registry.register_builtin(
    "generic",
    "app.drivers.generic.GenericDriver",
    "Generic Router (No DOCSIS)",
    hints={"credentials_required": False},
)


def load_driver(modem_type, url, user, password):
    """Backward-compatible wrapper around driver_registry.load_driver()."""
    return driver_registry.load_driver(modem_type, url, user, password)
