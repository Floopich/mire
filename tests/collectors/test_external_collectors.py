"""Tests for speedtest and backup collectors."""

"""Tests for the unified Collector Architecture."""

import os
import tempfile
import time
import pytest
from unittest.mock import MagicMock, patch, PropertyMock

from app.collectors.base import Collector, CollectorResult
from app.collectors.modem import ModemCollector
from app.modules.speedtest.collector import SpeedtestCollector
from app.drivers.base import ModemDriver


class TestSpeedtestCollector:
    def _make_collector(self, configured=True):
        config_mgr = MagicMock()
        config_mgr.is_speedtest_configured.return_value = configured
        config_mgr.get.side_effect = lambda k, *a: {
            "speedtest_tracker_url": "http://speed:8999",
            "speedtest_tracker_token": "tok",
        }.get(k, a[0] if a else None)

        storage = MagicMock()
        # Provide a real temp db_path so SpeedtestStorage can init
        storage.db_path = os.path.join(tempfile.mkdtemp(), "test.db")
        web = MagicMock()

        c = SpeedtestCollector(config_mgr=config_mgr, storage=storage, web=web, poll_interval=300)
        return c, config_mgr, storage, web

    def test_is_enabled_true(self):
        c, *_ = self._make_collector(configured=True)
        assert c.is_enabled() is True

    def test_is_enabled_false(self):
        c, *_ = self._make_collector(configured=False)
        assert c.is_enabled() is False

    @patch("app.modules.speedtest.collector.SpeedtestClient")
    def test_collect_initializes_client(self, mock_cls):
        mock_client = MagicMock()
        mock_client.get_latest_with_error.return_value = ([{"id": 1, "download_mbps": 100}], None)
        mock_client.get_results.return_value = []
        mock_cls.return_value = mock_client

        c, *_ = self._make_collector()
        c.collect()
        mock_cls.assert_called_once_with("http://speed:8999", "tok", tls_insecure=False)

    @patch("app.modules.speedtest.collector.SpeedtestClient")
    def test_collect_reinitializes_client_when_tls_setting_changes(self, mock_cls):
        mock_client = MagicMock()
        mock_client.get_latest_with_error.return_value = ([{"id": 1, "download_mbps": 100}], None)
        mock_client.get_results.return_value = []
        mock_cls.return_value = mock_client

        c, config_mgr, *_ = self._make_collector()
        c.collect()
        config_mgr.get.side_effect = lambda k, *a: {
            "speedtest_tracker_url": "http://speed:8999",
            "speedtest_tracker_token": "[REDACTED]",
            "speedtest_tls_insecure": True,
        }.get(k, a[0] if a else None)
        c.collect()

        assert mock_cls.call_args_list[-1].kwargs["tls_insecure"] is True
        assert mock_cls.call_count == 2

    @patch("app.modules.speedtest.collector.SpeedtestClient")
    def test_collect_updates_web_state(self, mock_cls):
        mock_client = MagicMock()
        mock_client.get_latest_with_error.return_value = ([{"id": 1}], None)
        mock_client.get_results.return_value = []
        mock_cls.return_value = mock_client

        c, _, _, web = self._make_collector()
        c.collect()
        web.update_state.assert_called_once()

    @patch("app.modules.speedtest.collector.SpeedtestClient")
    def test_collect_fetch_failure_returns_error(self, mock_cls):
        mock_client = MagicMock()
        mock_client.get_latest_with_error.return_value = ([], "ConnectionError: refused")
        mock_cls.return_value = mock_client

        c, _, _, web = self._make_collector()
        result = c.collect()
        assert result.success is False
        assert "ConnectionError" in result.error
        web.update_state.assert_not_called()

    @patch("app.modules.speedtest.collector.SpeedtestClient")
    def test_collect_delta_cache(self, mock_cls):
        mock_client = MagicMock()
        mock_client.get_latest_with_error.return_value = ([], None)
        mock_client.get_results.return_value = [
            {"id": 1, "timestamp": "2025-01-01T00:00:00Z", "download_mbps": 100,
             "upload_mbps": 10, "download_human": "", "upload_human": "",
             "ping_ms": 5, "jitter_ms": 1, "packet_loss_pct": 0},
            {"id": 2, "timestamp": "2025-01-01T01:00:00Z", "download_mbps": 200,
             "upload_mbps": 20, "download_human": "", "upload_human": "",
             "ping_ms": 5, "jitter_ms": 1, "packet_loss_pct": 0},
        ]
        mock_cls.return_value = mock_client

        c, _, storage, _ = self._make_collector()
        c.collect()
        # Verify results were saved to the module's internal storage
        assert c._storage.get_speedtest_count() == 2

    @patch("app.modules.speedtest.collector.SpeedtestClient")
    def test_collect_delta_cache_failure_does_not_crash(self, mock_cls):
        """Delta cache failure should not prevent a successful collect result."""
        mock_client = MagicMock()
        mock_client.get_latest_with_error.return_value = ([{"id": 1}], None)
        mock_client.get_results.side_effect = Exception("API timeout")
        mock_cls.return_value = mock_client

        c, _, storage, web = self._make_collector()
        result = c.collect()
        assert result.success is True
        web.update_state.assert_called_once()

    def test_name(self):
        c, *_ = self._make_collector()
        assert c.name == "speedtest"


# ── BackupCollector Tests ──


class TestBackupCollector:
    def _make_collector(self, configured=True, interval_hours=24, backups=None):
        config_mgr = MagicMock()
        config_mgr.is_backup_configured.return_value = configured
        config_mgr.data_dir = "/data"
        config_mgr.get.side_effect = lambda k, *a: {
            "backup_path": "/backup",
            "backup_interval_hours": interval_hours,
            "backup_retention": 5,
        }.get(k, a[0] if a else None)

        from app.modules.backup.collector import BackupCollector
        with patch("app.modules.backup.backup.list_backups", return_value=backups or []):
            c = BackupCollector(config_mgr=config_mgr)
        return c, config_mgr

    def test_name(self):
        c, _ = self._make_collector()
        assert c.name == "backup"

    def test_is_enabled(self):
        c, _ = self._make_collector(configured=True)
        assert c.is_enabled() is True
        c2, _ = self._make_collector(configured=False)
        assert c2.is_enabled() is False

    def test_interval_from_config(self):
        c, _ = self._make_collector(interval_hours=168)
        assert c._poll_interval_seconds == 168 * 3600

    def test_seed_last_poll_from_disk(self):
        """_last_poll is seeded from newest backup file on init."""
        from datetime import datetime, timedelta
        two_hours_ago = (datetime.now() - timedelta(hours=2)).isoformat()
        backups = [{"filename": "mire_backup_test.tar.gz", "size": 100, "modified": two_hours_ago}]
        c, _ = self._make_collector(backups=backups)
        # _last_poll should be close to 2h ago, not 0
        assert c._last_poll > 0
        age = time.time() - c._last_poll
        assert 7000 < age < 7400  # ~2h in seconds

    def test_seed_no_backups_leaves_last_poll_zero(self):
        """No backups on disk → _last_poll stays 0, first backup runs immediately."""
        c, _ = self._make_collector(backups=[])
        assert c._last_poll == 0.0

    def test_should_poll_false_after_seed(self):
        """Container restart with recent backup → should_poll() returns False."""
        from datetime import datetime, timedelta
        one_hour_ago = (datetime.now() - timedelta(hours=1)).isoformat()
        backups = [{"filename": "mire_backup_test.tar.gz", "size": 100, "modified": one_hour_ago}]
        c, _ = self._make_collector(interval_hours=24, backups=backups)
        assert c.should_poll() is False

    def test_should_poll_true_when_backup_expired(self):
        """Backup older than interval → should_poll() returns True."""
        from datetime import datetime, timedelta
        two_days_ago = (datetime.now() - timedelta(days=2)).isoformat()
        backups = [{"filename": "mire_backup_old.tar.gz", "size": 100, "modified": two_days_ago}]
        c, _ = self._make_collector(interval_hours=24, backups=backups)
        assert c.should_poll() is True

    def test_seed_includes_manual_backups(self):
        """Seed uses newest backup regardless of source (scheduled or manual).

        After a restart, _last_poll anchors to the newest file on disk.
        This means a manual backup can shift the automatic schedule, which
        is by design: the guarantee is "at least one backup every <interval>".
        """
        from datetime import datetime, timedelta
        one_hour_ago = (datetime.now() - timedelta(hours=1)).isoformat()
        backups = [{"filename": "mire_backup_2026-03-15_120000.tar.gz", "size": 100, "modified": one_hour_ago}]
        c, _ = self._make_collector(interval_hours=24, backups=backups)
        # _last_poll is seeded from the file, so should_poll() waits
        assert c.should_poll() is False

    @patch("app.modules.backup.backup.create_backup_to_file")
    @patch("app.modules.backup.backup.cleanup_old_backups")
    def test_collect_creates_backup(self, mock_cleanup, mock_create):
        mock_create.return_value = "mire_backup_2026-03-15.tar.gz"
        c, _ = self._make_collector()
        result = c.collect()
        assert result.success is True
        mock_create.assert_called_once()
        mock_cleanup.assert_called_once()
