"""Mot de passe administrateur genere au premier demarrage.

Sans mot de passe, `_auth_required` laisse tout passer, y compris
`/api/modules/install` qui execute du Python arbitraire. L'assistant de
configuration ne demandait jamais de mot de passe : une instance suivant
INSTALL.md se retrouvait ouverte sur 0.0.0.0:1340.

Un mot de passe par defaut partage (admin/admin) serait pire : la valeur
serait lisible dans un depot public et chaque instance non configuree
deviendrait attaquable avec un identifiant connu d'avance. On genere donc
une valeur aleatoire propre a l'instance, qu'on ne revele que par des
canaux exigeant un acces a l'hote : les logs du conteneur et un fichier en
mode 0600. Elle n'est jamais affichee dans une page web, l'assistant et le
login etant par construction accessibles sans authentification.
"""

from __future__ import annotations

import logging
import os
import secrets
import stat

LOG = logging.getLogger(__name__)

INITIAL_PASSWORD_FILENAME = ".initial_password"
_TOKEN_BYTES = 12


def initial_password_path(data_dir: str) -> str:
    return os.path.join(data_dir, INITIAL_PASSWORD_FILENAME)


def clear_initial_password(data_dir: str) -> bool:
    """Effacer la trace du mot de passe genere. Appele des que l'utilisateur
    definit le sien : le fichier ne doit pas survivre au mot de passe."""
    path = initial_password_path(data_dir)
    try:
        os.remove(path)
    except FileNotFoundError:
        return False
    except OSError as exc:
        LOG.warning("Could not remove %s: %s", INITIAL_PASSWORD_FILENAME, type(exc).__name__)
        return False
    return True


def _write_secret_file(path: str, value: str) -> bool:
    """Ecriture atomique en 0600, comme .session_key et .auth_state."""
    tmp_path = f"{path}.tmp"
    try:
        flags = os.O_WRONLY | os.O_CREAT | os.O_TRUNC
        handle = os.open(tmp_path, flags, stat.S_IRUSR | stat.S_IWUSR)
        try:
            os.write(handle, value.encode("utf-8"))
        finally:
            os.close(handle)
        os.replace(tmp_path, path)
        os.chmod(path, stat.S_IRUSR | stat.S_IWUSR)
        return True
    except OSError as exc:
        LOG.warning("Could not store the initial password: %s", type(exc).__name__)
        try:
            os.remove(tmp_path)
        except OSError:
            pass
        return False


def ensure_admin_password(config_manager) -> str | None:
    """Generer un mot de passe si aucun n'est defini. Renvoie la valeur en
    clair quand elle vient d'etre creee, None si un mot de passe existait."""
    if config_manager is None:
        return None
    try:
        existing = config_manager.get("admin_password", "")
    except Exception:
        return None
    if existing:
        # Un mot de passe existe (config ou ADMIN_PASSWORD) : rien a faire.
        return None

    password = secrets.token_urlsafe(_TOKEN_BYTES)
    try:
        config_manager.save({"admin_password": password})
    except Exception as exc:
        LOG.error("Could not set an initial admin password: %s", type(exc).__name__)
        return None

    data_dir = getattr(config_manager, "data_dir", "")
    stored = _write_secret_file(initial_password_path(data_dir), password + "\n") if data_dir else False

    LOG.warning(
        "No admin password was set. Mire generated one for this instance: %s", password
    )
    LOG.warning(
        "Change it in Settings. %s",
        f"It is also readable in {INITIAL_PASSWORD_FILENAME} inside the data directory."
        if stored
        else "Store it now: it is not written anywhere else.",
    )
    return password
