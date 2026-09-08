"""Generic module downloader for fetching community modules from GitHub."""

import json
import logging
import os
import shutil
import urllib.request
from urllib.parse import urlparse

log = logging.getLogger("docsis.module_download")

REQUIRED_ENTRY_FIELDS = {"id", "name", "version", "download_url", "min_app_version"}

TRUSTED_HOSTS = {
    "raw.githubusercontent.com",
    "api.github.com",
    "github.com",
}

# Plafonds : le collecteur itinerant tourne sur un Raspberry Pi 3B+ (1 Go).
MAX_REGISTRY_BYTES = 1 * 1024 * 1024
MAX_FILE_BYTES = 5 * 1024 * 1024
MAX_TOTAL_BYTES = 25 * 1024 * 1024
MAX_DIR_DEPTH = 8


class _TrustedRedirectHandler(urllib.request.HTTPRedirectHandler):
    """Revalide l'allowlist a chaque saut.

    is_trusted_url() ne verifie que l'URL initiale ; urlopen suit les
    redirections par defaut, donc un hote de confiance pouvait renvoyer
    vers n'importe quel autre hote.
    """

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        if not is_trusted_url(newurl):
            raise urllib.error.HTTPError(
                newurl, code, "redirect to untrusted host", headers, fp
            )
        return super().redirect_request(req, fp, code, msg, headers, newurl)


_OPENER = urllib.request.build_opener(_TrustedRedirectHandler)


def _read_capped(url: str, timeout: int, limit: int) -> bytes:
    """Telecharge en refusant tout depassement du plafond.

    Lit limit + 1 octets : Content-Length est declaratif et ne peut pas
    servir de garde a lui seul.
    """
    with _OPENER.open(url, timeout=timeout) as resp:
        payload = resp.read(limit + 1)
    if len(payload) > limit:
        raise ValueError(f"payload exceeds {limit} bytes")
    return payload


def safe_url_label(url: str | None) -> str:
    """Return a log-safe source label without user-controlled URL detail."""
    if not url:
        return "<empty-url>"
    try:
        parsed = urlparse(url)
        if not parsed.scheme or not parsed.hostname:
            return "<invalid-url>"
        _ = parsed.port  # validate malformed/out-of-range ports without logging them
        # Return only fixed labels for known hosts. Unknown hosts may be private
        # instance data, so keep them out of shareable logs.
        if parsed.hostname == "raw.githubusercontent.com":
            return "raw.githubusercontent.com"
        if parsed.hostname == "api.github.com":
            return "api.github.com"
        if parsed.hostname == "github.com":
            return "github.com"
    except (TypeError, ValueError):
        return "<invalid-url>"
    return "<custom-endpoint>"


def is_trusted_url(url: str) -> bool:
    """Check that a URL uses HTTPS and points to a trusted GitHub host."""
    try:
        parsed = urlparse(url)
        return parsed.scheme == "https" and parsed.hostname in TRUSTED_HOSTS
    except Exception:
        return False


def validate_registry_entry(entry: dict[str, str]) -> bool:
    """Check if a registry entry has all required fields."""
    return REQUIRED_ENTRY_FIELDS.issubset(entry.keys())


def fetch_registry(registry_url: str, key: str = "modules", timeout: int = 10) -> list[dict[str, str]]:
    """Fetch a registry index and return list of valid entries.

    Args:
        registry_url: URL to the registry JSON file
        key: top-level key in the JSON (e.g., "modules" or "themes")
        timeout: request timeout in seconds
    """
    if not is_trusted_url(registry_url):
        log.error("Refusing registry fetch: untrusted endpoint %s", safe_url_label(registry_url))
        return []
    try:
        data = json.loads(_read_capped(registry_url, timeout, MAX_REGISTRY_BYTES))
        entries = data.get(key, [])
        return [e for e in entries if validate_registry_entry(e)]
    except Exception as e:
        log.warning("Failed to fetch registry from %s: %s", safe_url_label(registry_url), type(e).__name__)
        return []


def download_github_directory(
    download_url: str,
    target_dir: str,
    timeout: int = 30,
    _depth: int = 0,
    _budget: list[int] | None = None,
) -> bool:
    """Download a directory recursively from the GitHub Contents API.

    Fully recursive traversal of any directory structure. All URLs are
    validated against TRUSTED_HOSTS to prevent SSRF.

    Args:
        download_url: GitHub Contents API URL for the directory
        target_dir: local directory to download into
        timeout: request timeout in seconds

    Returns:
        True on success, False on failure (target_dir is cleaned up on failure)
    """
    if not is_trusted_url(download_url):
        log.error("Refusing download: untrusted endpoint %s", safe_url_label(download_url))
        return False

    if _depth > MAX_DIR_DEPTH:
        log.error("Refusing download: directory nesting exceeds %d levels", MAX_DIR_DEPTH)
        return False

    budget = [MAX_TOTAL_BYTES] if _budget is None else _budget

    try:
        os.makedirs(target_dir, exist_ok=True)

        entries = json.loads(_read_capped(download_url, timeout, MAX_REGISTRY_BYTES))

        for entry in entries:
            name = os.path.basename(entry.get("name", ""))
            entry_type = entry.get("type", "")

            if not name or name in (".", ".."):
                log.warning("Skipping suspicious entry name: %r", entry.get("name"))
                continue

            candidate = os.path.realpath(os.path.join(target_dir, name))
            if not candidate.startswith(os.path.realpath(target_dir) + os.sep):
                log.warning("Path traversal blocked: %r", entry.get("name"))
                continue

            if entry_type == "file":
                file_url = entry.get("download_url")
                if not file_url or not is_trusted_url(file_url):
                    log.warning("Skipping untrusted file endpoint: %s", safe_url_label(file_url))
                    continue
                payload = _read_capped(
                    file_url, timeout, min(MAX_FILE_BYTES, budget[0])
                )
                budget[0] -= len(payload)
                with open(candidate, "wb") as f:
                    f.write(payload)

            elif entry_type == "dir":
                subdir_url = entry.get("url", "")
                if not is_trusted_url(subdir_url):
                    log.warning("Skipping untrusted dir endpoint: %s", safe_url_label(subdir_url))
                    continue
                if not download_github_directory(
                    subdir_url, candidate, timeout, _depth + 1, budget
                ):
                    shutil.rmtree(target_dir, ignore_errors=True)
                    return False

        return True
    except Exception as e:
        log.error("Failed to download from %s: %s", safe_url_label(download_url), type(e).__name__)
        shutil.rmtree(target_dir, ignore_errors=True)
        return False
