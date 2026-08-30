"""Tests for the builtin modem drivers shipped with Mire.

Mire ships exactly two builtin drivers: ``voo_cga4233`` (Technicolor CGA4233 on
the VOO firmware) and ``generic`` (no DOCSIS data, router fallback).
"""

import pytest

from app.drivers.base import ModemDriver
from app.drivers.generic import GenericDriver
from app.drivers.voo_cga4233 import VooCGA4233Driver


class TestLoadDriver:
    def test_load_voo_cga4233_driver(self):
        from app.drivers import load_driver

        driver = load_driver("voo_cga4233", "http://192.168.100.1", "admin", "pass")
        assert isinstance(driver, VooCGA4233Driver)

    def test_load_generic_driver(self):
        from app.drivers import load_driver

        driver = load_driver("generic", "http://192.168.100.1", "", "")
        assert isinstance(driver, GenericDriver)

    def test_unknown_driver_raises(self):
        from app.drivers import load_driver

        with pytest.raises(ValueError, match="Unknown modem_type"):
            load_driver("nonexistent", "http://x", "u", "p")

    def test_registry_exposes_exactly_the_shipped_drivers(self):
        from app.drivers import driver_registry

        assert driver_registry.has_driver("voo_cga4233")
        assert driver_registry.has_driver("generic")

    @pytest.mark.parametrize(
        "bad_type",
        [
            "../../etc/passwd",
            "__import__('os')",
            "",
            "voo_cga4233; import os",
            "../drivers/voo_cga4233",
        ],
    )
    def test_malicious_modem_type_rejected(self, bad_type):
        from app.drivers import load_driver

        with pytest.raises(ValueError, match="Unknown modem_type"):
            load_driver(bad_type, "http://x", "u", "p")


class TestBuiltinDriverContract:
    @pytest.mark.parametrize("driver_cls", [VooCGA4233Driver, GenericDriver])
    def test_driver_subclasses_modem_driver(self, driver_cls):
        assert issubclass(driver_cls, ModemDriver)

    @pytest.mark.parametrize("driver_cls", [VooCGA4233Driver, GenericDriver])
    def test_driver_implements_required_api(self, driver_cls):
        driver = driver_cls("http://192.168.100.1", "admin", "pass")

        for method_name in (
            "login",
            "get_docsis_data",
            "get_device_info",
            "get_connection_info",
        ):
            assert callable(getattr(driver, method_name))

    @pytest.mark.parametrize("driver_cls", [VooCGA4233Driver, GenericDriver])
    def test_driver_stores_credentials(self, driver_cls):
        driver = driver_cls("http://192.168.100.1", "admin", "secret")

        assert driver._url == "http://192.168.100.1"
        assert driver._user == "admin"
        assert driver._password == "secret"


class TestVooDriverHints:
    """Les valeurs pre-remplies du formulaire doivent correspondre au firmware."""

    def test_voo_hints_match_the_voo_firmware(self):
        from app.drivers import driver_registry

        hints = driver_registry.get_driver_hints()["voo_cga4233"]
        assert hints["default_url"] == "http://192.168.100.1"
        # Le firmware VOO rejette "admin" (MSG_LOGIN_1) ; l'utilisateur est "voo".
        assert hints["default_user"] == "voo"
