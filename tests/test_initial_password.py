"""Le mot de passe genere au premier demarrage."""

from __future__ import annotations

import os
import stat

import pytest

from app.config import ConfigManager
from app.initial_password import (
    INITIAL_PASSWORD_FILENAME,
    clear_initial_password,
    ensure_admin_password,
    initial_password_path,
)


@pytest.fixture()
def manager(tmp_path):
    return ConfigManager(data_dir=str(tmp_path))


def test_a_password_is_generated_when_none_is_set(manager):
    password = ensure_admin_password(manager)

    assert password
    assert len(password) >= 12
    stored = manager.get("admin_password", "")
    assert stored.startswith(("scrypt:", "pbkdf2:"))
    assert password not in stored


def test_generated_passwords_differ_between_instances(tmp_path):
    first = ensure_admin_password(ConfigManager(data_dir=str(tmp_path / "a")))
    second = ensure_admin_password(ConfigManager(data_dir=str(tmp_path / "b")))

    assert first != second


def test_an_existing_password_is_never_replaced(manager):
    manager.save({"admin_password": "chosen-by-the-user"})
    before = manager.get("admin_password", "")

    assert ensure_admin_password(manager) is None
    assert manager.get("admin_password", "") == before


def test_the_plaintext_file_is_owner_readable_only(manager, tmp_path):
    password = ensure_admin_password(manager)
    path = initial_password_path(str(tmp_path))

    assert os.path.exists(path)
    assert open(path, encoding="utf-8").read().strip() == password
    mode = stat.S_IMODE(os.stat(path).st_mode)
    assert mode == stat.S_IRUSR | stat.S_IWUSR


def test_choosing_a_password_removes_the_generated_trace(manager, tmp_path):
    ensure_admin_password(manager)
    assert os.path.exists(initial_password_path(str(tmp_path)))

    manager.save({"admin_password": "chosen-by-the-user"})

    assert not os.path.exists(initial_password_path(str(tmp_path)))


def test_clearing_is_idempotent(tmp_path):
    assert clear_initial_password(str(tmp_path)) is False


def test_the_plaintext_never_reaches_config_json(manager, tmp_path):
    """Seul le hachage est persiste : config.json ne doit pas livrer la valeur."""
    password = ensure_admin_password(manager)

    on_disk = (tmp_path / "config.json").read_text(encoding="utf-8")

    assert password not in on_disk
    assert INITIAL_PASSWORD_FILENAME not in on_disk
