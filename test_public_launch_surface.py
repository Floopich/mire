"""Static contracts for the Mire public documentation surface."""

from __future__ import annotations

import re
import struct
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DOCS = ROOT / "docs"
README = ROOT / "README.md"
DATA_CONTRACT = ROOT / "DATA_CONTRACT.md"
UNLINKED_PUBLIC_IMAGES = [
    DOCS / "mire.png",
    DOCS / "screenshots" / "setup.png",
    DOCS / "screenshots" / "smart-capture-settings.png",
    DOCS / "screenshots" / "readme-hero-evidence.png",
]
RE_LINK = re.compile(r"\]\(([^)\s]+)\)")
LOCAL_PUBLIC_ASSET_RE = re.compile(
    r"(?<![\w/-])(?:docs/)?(?:screenshots/|samples/)?[A-Za-z0-9_.-]+\.(?:png|jpg|jpeg|webp|svg|pdf)"
)


def png_size(path: Path) -> tuple[int, int]:
    with path.open("rb") as fh:
        assert fh.read(8) == b"\x89PNG\r\n\x1a\n"
        length = struct.unpack(">I", fh.read(4))[0]
        assert fh.read(4) == b"IHDR"
        width, height = struct.unpack(">II", fh.read(8))
        assert length == 13
        return width, height


def test_public_surface_docs_and_social_asset_exist() -> None:
    expected = [
        DATA_CONTRACT,
        DOCS / "feature-matrix.md",
        DOCS / "proof-pack.md",
        DOCS / "samples" / "demo-complaint-report.pdf",
        DOCS / "mire-logo.svg",
    ]
    for path in expected:
        assert path.exists(), path
        assert path.stat().st_size > 0, path

    # Garde-fou : aucune capture ne revient tant qu'elle ne vient pas de Mire.
    assert list(DOCS.glob("screenshots/*.png")) == []


def test_public_docs_reference_existing_local_assets_without_unlinked_images() -> None:
    public_docs = [README, *sorted(DOCS.rglob("*.md"))]

    missing = []
    for source in public_docs:
        for ref in sorted(set(LOCAL_PUBLIC_ASSET_RE.findall(source.read_text(encoding="utf-8")))):
            asset = ROOT / ref if ref.startswith("docs/") else DOCS / ref
            if not asset.exists():
                missing.append(f"{source.relative_to(ROOT)} -> {ref}")

    assert missing == []
    assert [path.relative_to(ROOT).as_posix() for path in UNLINKED_PUBLIC_IMAGES if path.exists()] == []


def test_public_docs_have_no_dead_relative_links() -> None:
    sources = [*sorted(ROOT.glob("*.md")), *sorted(DOCS.rglob("*.md"))]

    dead = []
    for source in sources:
        for link in re.findall(RE_LINK, source.read_text(encoding="utf-8")):
            if link.startswith(("http://", "https://", "mailto:", "#")):
                continue
            if not (source.parent / link.split("#", 1)[0]).exists():
                dead.append(f"{source.relative_to(ROOT)} -> {link}")

    assert dead == []


def test_no_private_or_localhost_values_in_public_surface() -> None:
    # Le README documente legitimement l'adresse standard du modem : le
    # garde-fou vise les documents de presentation, pas la doc d'installation.
    paths = [DOCS / "feature-matrix.md"]
    pattern = re.compile(r"(localhost|127\.0\.0\.1|192\.168\.|10\.|172\.(1[6-9]|2\d|3[01])\.|Vodafone Kabel)", re.I)
    for path in paths:
        assert not pattern.search(path.read_text(encoding="utf-8")), path


def test_public_modem_family_counts_match_registry() -> None:
    from app.drivers import driver_registry

    families = driver_registry.get_all_type_keys() - {"generic"}

    # Le README nomme les modeles plutot qu'il n'en annonce le compte : une
    # tournure figee obligeait a ecrire "1 modem pris en charge" en toutes
    # lettres. Nommer chaque famille garde la meme garantie sans dicter la
    # redaction, et signale aussi bien un modele ajoute qu'un modele retire.
    readme = README.read_text(encoding="utf-8")
    models = {
        label for key, label in driver_registry.get_available_drivers()
        if key in families
    }
    missing = sorted(model for model in models if model not in readme)
    assert missing == [], missing
