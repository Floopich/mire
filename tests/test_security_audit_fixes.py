"""Tests for security audit findings: SSRF, restore rate-limit, XSS filter."""

import io
import json
import tempfile
from unittest.mock import MagicMock, patch

import pytest

from app.module_download import fetch_registry, is_trusted_url


# ── Finding 1: SSRF via registry URL ──


class TestFetchRegistrySSRF:
    """fetch_registry must reject untrusted URLs."""

    def test_rejects_http_url(self):
        result = fetch_registry("http://evil.com/registry.json")
        assert result == []

    def test_rejects_internal_url(self):
        result = fetch_registry("http://169.254.169.254/latest/meta-data/")
        assert result == []

    def test_rejects_file_url(self):
        result = fetch_registry("file:///etc/passwd")
        assert result == []

    def test_rejects_localhost(self):
        result = fetch_registry("http://localhost:8080/registry.json")
        assert result == []

    def test_rejects_untrusted_https(self):
        result = fetch_registry("https://evil.com/registry.json")
        assert result == []

    @patch("app.module_download._OPENER.open")
    def test_allows_trusted_github_url(self, mock_urlopen):
        registry_data = {"modules": [
            {"id": "test", "name": "Test", "version": "1.0",
             "download_url": "https://github.com/x", "min_app_version": "1.0"}
        ]}
        mock_resp = MagicMock()
        mock_resp.read.return_value = json.dumps(registry_data).encode()
        mock_resp.__enter__ = lambda s: s
        mock_resp.__exit__ = MagicMock(return_value=False)
        mock_urlopen.return_value = mock_resp

        result = fetch_registry(
            "https://raw.githubusercontent.com/user/repo/main/registry.json"
        )
        assert len(result) == 1
        assert result[0]["id"] == "test"

    @patch("app.module_download._OPENER.open")
    def test_allows_github_api_url(self, mock_urlopen):
        registry_data = {"themes": [
            {"id": "t1", "name": "Theme", "version": "1.0",
             "download_url": "https://github.com/x", "min_app_version": "1.0"}
        ]}
        mock_resp = MagicMock()
        mock_resp.read.return_value = json.dumps(registry_data).encode()
        mock_resp.__enter__ = lambda s: s
        mock_resp.__exit__ = MagicMock(return_value=False)
        mock_urlopen.return_value = mock_resp

        result = fetch_registry(
            "https://api.github.com/repos/user/repo/contents/registry.json",
            key="themes",
        )
        assert len(result) == 1


# ── Finding 2: Restore rate-limiting ──


class TestRestoreRateLimit:
    """Unauthenticated restore endpoints must be rate-limited."""

    @pytest.fixture
    def client(self):
        from app.app_factory import create_app
        from app.config import ConfigManager
        from app.modules.backup.routes import bp as backup_bp

        with tempfile.TemporaryDirectory() as td:
            cfg = ConfigManager(td)
            cfg.save({"update_check_enabled": False})
            test_app = create_app(config_manager=cfg, environ={}, testing=True)
            test_app.register_blueprint(backup_bp)

            # Patch the getters that backup routes use
            with patch("app.modules.backup.routes.get_config_manager", return_value=cfg), \
                 patch("app.modules.backup.routes._auth_required", return_value=False), \
                 patch("app.modules.backup.routes._get_client_ip", return_value="127.0.0.1"):
                with test_app.test_client() as c:
                    yield c

    def test_restore_validate_rate_limited(self, client):
        """After 5 attempts, further unauthenticated restore/validate should be blocked."""
        for i in range(5):
            resp = client.post(
                "/api/restore/validate",
                data={"file": (io.BytesIO(b"dummy"), "backup.tar.gz")},
                content_type="multipart/form-data",
            )
            assert resp.status_code != 429, f"Blocked too early on attempt {i+1}"

        # 6th attempt should be rate-limited
        resp = client.post(
            "/api/restore/validate",
            data={"file": (io.BytesIO(b"dummy"), "backup.tar.gz")},
            content_type="multipart/form-data",
        )
        assert resp.status_code == 429

    def test_restore_rate_limited(self, client):
        """After 5 attempts, further unauthenticated restore should be blocked."""
        for i in range(5):
            client.post(
                "/api/restore",
                data={"file": (io.BytesIO(b"dummy"), "backup.tar.gz")},
                content_type="multipart/form-data",
            )

        resp = client.post(
            "/api/restore",
            data={"file": (io.BytesIO(b"dummy"), "backup.tar.gz")},
            content_type="multipart/form-data",
        )
        assert resp.status_code == 429

    def test_restore_without_config_manager_rejected(self):
        """If the config manager is not initialized, /api/restore must bail out
        before touching the filesystem — no fallback to a hardcoded data dir."""
        from app.app_factory import create_app
        from app.config import ConfigManager
        from app.runtime import get_runtime
        from app.modules.backup import routes as br
        from app.modules.backup.routes import bp as backup_bp

        test_app = create_app(
            config_manager=ConfigManager(tempfile.mkdtemp()), environ={}, testing=True
        )

        with patch("app.modules.backup.routes.get_config_manager", return_value=None), \
             patch("app.modules.backup.routes._auth_required", return_value=False), \
             patch("app.modules.backup.routes._get_client_ip", return_value="127.0.0.1"), \
             patch("app.modules.backup.routes.restore_backup") as mock_restore:
            test_app.register_blueprint(backup_bp)
            with test_app.test_client() as c:
                resp = c.post(
                    "/api/restore",
                    data={"file": (io.BytesIO(b"dummy"), "backup.tar.gz")},
                    content_type="multipart/form-data",
                )
            assert resp.status_code == 500
            assert resp.get_json() == {"error": "Not initialized"}
            # The missing guard previously let restore_backup run against "/data".
            mock_restore.assert_not_called()
            # Must not count as a rate-limit attempt either — the request never
            # reached the rate-limited code path.
            assert get_runtime(test_app).derived_storage.value("backup_restore_attempts", {}) == {}

    def test_authenticated_restore_not_rate_limited(self):
        """Configured+authenticated instances skip the rate limit path entirely."""
        from app.app_factory import create_app
        from app.config import ConfigManager
        from app.modules.backup import routes as br

        with tempfile.TemporaryDirectory() as td:
            cfg = ConfigManager(td)
            cfg.save({"admin_password": "testpass", "modem_type": "demo"})
            test_app = create_app(config_manager=cfg, environ={}, testing=True)

            with patch("app.modules.backup.routes.get_config_manager", return_value=cfg), \
                 patch("app.modules.backup.routes._auth_required", return_value=False), \
                 patch("app.modules.backup.routes._get_client_ip", return_value="127.0.0.1"):
                from app.modules.backup.routes import bp as backup_bp
                test_app.register_blueprint(backup_bp)
                with test_app.test_client() as c:
                    for i in range(10):
                        resp = c.post(
                            "/api/restore/validate",
                            data={"file": (io.BytesIO(b"dummy"), "backup.tar.gz")},
                            content_type="multipart/form-data",
                        )
                        # Configured instance skips rate-limit code path
                        assert resp.status_code != 429, f"Rate-limited on attempt {i+1}"


