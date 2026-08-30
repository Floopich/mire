"""DOCSIS driver for the Technicolor CGA4233 on VOO (Belgium) firmware.

Autonome : hérite directement de ModemDriver, sans dépendance à un autre
driver. La séquence d'authentification double PBKDF2-SHA256 est celle des
unités CGA (documentation ZahrtheMad CGA6444VF, référence fthomys), reprise
telle quelle depuis le driver Vodafone Station de l'amont.

Les endpoints /api/v1/sta_docsis_status et /api/v1/sta_device_info de l'amont
n'existent pas sur ce firmware et répondent HTTP 500 ; les données DOCSIS
passent par /api/v1/modem/*.

Le parsing des payloads vit dans app/drivers/formats/voo.py (profil
voo_cga4233_json) : ce driver ne fait que transport, authentification et
délégation.

Verified against firmware CGA4233VOO, bridge mode, 2026-08.
"""

from __future__ import annotations

import logging

import requests

from .base import ModemDriver
from .formats.voo import parse_voo_cga4233_json
from .utils import pbkdf2_sha256
from ..types import ConnectionInfo, DeviceInfo, DocsisData

log = logging.getLogger("docsis.driver.voo_cga4233")

DOCSIS_PATH = "/api/v1/modem/exUSTbl,exDSTbl,USTbl,DSTbl,ErrTbl"


class VooCGA4233Driver(ModemDriver):
    """Technicolor CGA4233 on VOO firmware, bridge or router mode."""

    FORMAT_FAMILIES = ("voo_cga4233_json",)

    def __init__(self, url: str, user: str, password: str):
        super().__init__(url, user, password)
        self._session = requests.Session()
        self._cga_token = None

    def login(self) -> None:
        """Authenticate, then recover the CSRF token this firmware requires."""
        self._login_cga()
        if not self._cga_token:
            self._recover_csrf_token()

    def _login_cga(self) -> None:
        """Double PBKDF2-SHA256 auth.

        1. En-têtes de session + cookie cwd=No
        2. POST seeksalthash+logout=true -> salt + saltwebui
        3. hash1 = PBKDF2(password, salt, 1000, 16).hex()
        4. hash2 = PBKDF2(hash1, saltwebui, 1000, 16).hex()
        5. POST hash2 + logout=true
        6. Initialisation de session via /api/v1/session/menu
        """
        if self._cga_token and self._session.cookies:
            log.debug("CGA session active, skipping login")
            return

        self._session.cookies.clear()
        self._cga_token = None

        # Le firmware CGA verifie User-Agent, X-Requested-With et Referer sur
        # chaque requete, y compris l'initialisation de /session/menu.
        self._session.headers.update({
            "User-Agent": "Mozilla/5.0",
            "X-Requested-With": "XMLHttpRequest",
            "Referer": f"{self._url}/",
        })
        self._session.cookies.set("cwd", "No")

        form_headers = {
            "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
        }

        r1 = self._session.post(
            f"{self._url}/api/v1/session/login",
            data={
                "username": self._user,
                "password": "seeksalthash",
                "logout": "true",
            },
            headers=form_headers,
            timeout=10,
        )
        r1.raise_for_status()
        salt_data = r1.json()

        salt = salt_data.get("salt", "")
        salt_webui = salt_data.get("saltwebui", "")
        if not salt or not salt_webui:
            raise RuntimeError(f"CGA: No salt/saltwebui in response (got: {salt_data})")

        hash1 = pbkdf2_sha256(
            self._password.encode("utf-8"),
            salt.encode("utf-8"),
        ).hex()
        hash2 = pbkdf2_sha256(
            hash1.encode("utf-8"),
            salt_webui.encode("utf-8"),
        ).hex()

        r2 = self._session.post(
            f"{self._url}/api/v1/session/login",
            data={
                "username": self._user,
                "password": hash2,
                "logout": "true",
            },
            headers=form_headers,
            timeout=10,
        )
        r2.raise_for_status()
        login_data = r2.json()

        error = login_data.get("error")
        if error and error != "ok":
            raise RuntimeError(f"CGA login error: {error}")

        self._cga_token = login_data.get("token", "")

        try:
            r3 = self._session.get(f"{self._url}/api/v1/session/menu", timeout=10)
            log.debug("CGA menu init: HTTP %s", r3.status_code)
        except Exception as e:
            log.debug("CGA menu init failed (non-critical): %s", e)

        log.info("CGA auth OK (cookies: %s, token: %s)",
                 list(self._session.cookies.keys()),
                 "present" if self._cga_token else "absent")

    def _invalidate_cga_session(self) -> None:
        """Clear session state and session-level headers."""
        self._cga_token = None
        self._session.cookies.clear()
        for key in ("User-Agent", "X-Requested-With", "Referer"):
            self._session.headers.pop(key, None)

    def _recover_csrf_token(self) -> None:
        """Take the CSRF token from the `auth` cookie.

        This firmware returns no "token" field in the login response, so
        _cga_token stays empty and X-CSRF-TOKEN is never sent -- which the
        modem/* endpoints require. The web UI reads the value straight out of the
        `auth` cookie set at login and replays it as the header, so we do the
        same.
        """
        token = self._session.cookies.get("auth")
        if token:
            self._cga_token = token
            log.info("CSRF token taken from auth cookie")
        else:
            log.warning("No auth cookie; modem/* calls will return 401")

    def _cga_request(self, method: str, path: str, **kwargs):
        """Authenticated request. Timeout 30s.

        Ce firmware met ~9 s a assembler les tables DOCSIS, ce qui faisait
        echouer les collectes par intermittence sur un timeout de lecture de 10 s.
        """
        import time as _time

        headers = kwargs.pop("headers", {})
        if self._cga_token:
            headers["X-CSRF-TOKEN"] = self._cga_token
        params = kwargs.pop("params", {})
        if method.upper() == "GET":
            params["_"] = int(_time.time() * 1000)
        kwargs.pop("timeout", None)
        r = self._session.request(
            method,
            f"{self._url}{path}",
            headers=headers,
            params=params,
            timeout=30,
            **kwargs,
        )
        r.raise_for_status()
        return r

    def _fetch(self, path: str) -> dict:
        """GET an authenticated endpoint, retrying once on a stale session.

        Seuls 400/401/403 justifient une re-authentification. Les autres echecs
        sont remontes sans bruler une seconde requete et un cycle de login
        complet a chaque collecte.
        """
        try:
            return self._cga_request("GET", path).json()
        except requests.RequestException as exc:
            status = getattr(getattr(exc, "response", None), "status_code", None)
            self._invalidate_cga_session()
            if status not in (400, 401, 403):
                raise RuntimeError(f"VOO CGA4233: {path} failed: {exc}") from exc
            log.warning("%s failed (%s), re-authenticating and retrying", path, status)
            try:
                self.login()
                return self._cga_request("GET", path).json()
            except Exception as retry_exc:
                self._invalidate_cga_session()
                raise RuntimeError(
                    f"VOO CGA4233: {path} failed after retry: {retry_exc}"
                ) from retry_exc

    def get_docsis_data(self) -> DocsisData:
        """Fetch one payload and delegate parsing to the voo_cga4233_json profile."""
        result = parse_voo_cga4233_json(self._fetch(DOCSIS_PATH))
        data = result.value
        log.debug(
            "VOO CGA4233: DS %d SC-QAM + %d OFDM, US %d SC-QAM + %d OFDMA",
            len(data["channelDs"]["docsis30"]), len(data["channelDs"]["docsis31"]),
            len(data["channelUs"]["docsis30"]), len(data["channelUs"]["docsis31"]),
        )
        return data

    def get_connection_info(self) -> ConnectionInfo:
        """Le modem n expose aucune information WAN en mode bridge.

        Les debits contractuels viennent des reglages booked_download /
        booked_upload (Parametres > Speedtest, ou BOOKED_DOWNLOAD /
        BOOKED_UPLOAD), propres a chaque abonne. Le rapport s en sert en repli.
        """
        return {}

    def get_device_info(self) -> DeviceInfo:
        """Static device info.

        This firmware exposes no reachable device-info endpoint: /api/v1/system/*
        answers 401 even inside an authenticated session, and /sta_device_info
        does not exist. Probing it on every poll burned a full re-login cycle and
        exhausted the modem session before the DOCSIS fetch could run, so nothing
        is requested here.
        """
        return {
            "manufacturer": "Technicolor",
            "model": "CGA4233 (VOO)",
        }
