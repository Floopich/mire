"""Un onglet de module ne doit pas exposer des elements que rien ne pilote.

Les deux modules belges ont vecu plusieurs versions avec un bouton Calculer
inerte et des conteneurs de resultats jamais remplis : les routes existaient,
le front-end n'avait jamais ete ecrit et rien ne le signalait.
"""

from __future__ import annotations

import json
import pathlib
import re

ROOT = pathlib.Path(__file__).resolve().parents[1]
MODULES = ROOT / "app" / "modules"
CORE_JS = ROOT / "app" / "static" / "js"

BUTTON_ID_RE = re.compile(r'<button[^>]*\bid="([^"]+)"')
EMPTY_DIV_ID_RE = re.compile(r'<div[^>]*\bid="([^"]+)"[^>]*>\s*</div>')
ONCLICK_RE = re.compile(r'\bonclick="')


def _js_sources(module: pathlib.Path) -> str:
    sources = [path.read_text(encoding="utf-8") for path in module.rglob("*.js")]
    sources += [path.read_text(encoding="utf-8") for path in CORE_JS.rglob("*.js")]
    return "\n".join(sources)


def _element_html(html: str, element_id: str) -> str:
    """Le fragment de balise portant cet identifiant, pour y chercher onclick."""
    match = re.search(r"<[^>]*\bid=\"" + re.escape(element_id) + r"\"[^>]*>", html)
    return match.group(0) if match else ""


def test_module_tabs_have_no_dead_controls() -> None:
    orphans = []
    for template in sorted(MODULES.glob("*/templates/*.html")):
        module = template.parents[1]
        html = template.read_text(encoding="utf-8")
        scripts = _js_sources(module)
        ids = set(BUTTON_ID_RE.findall(html)) | set(EMPTY_DIV_ID_RE.findall(html))
        for element_id in sorted(ids):
            if "{{" in element_id:
                continue
            if element_id in scripts:
                continue
            if ONCLICK_RE.search(_element_html(html, element_id)):
                continue
            orphans.append(f"{template.relative_to(ROOT)} -> #{element_id}")

    assert orphans == []


def test_modules_shipping_js_declare_their_static_folder() -> None:
    undeclared = []
    for manifest_path in sorted(MODULES.glob("*/manifest.json")):
        module = manifest_path.parent
        if not list(module.rglob("*.js")):
            continue
        contributes = json.loads(manifest_path.read_text(encoding="utf-8")).get("contributes", {})
        if "static" not in contributes:
            undeclared.append(str(manifest_path.relative_to(ROOT)))

    assert undeclared == []


def test_module_init_functions_match_the_dashboard_router() -> None:
    """Le routeur derive le nom depuis l'identifiant du module.

    view mod-mire-be_compensation -> initBe_compensation. Un nom qui s'en
    ecarte laisse l'onglet muet sans lever d'erreur.
    """
    missing = []
    for manifest_path in sorted(MODULES.glob("*/manifest.json")):
        module = manifest_path.parent
        main_js = module / "static" / "main.js"
        if not main_js.exists():
            continue
        module_id = json.loads(manifest_path.read_text(encoding="utf-8"))["id"]
        suffix = module_id.split(".", 1)[1]
        expected = "init" + "".join(
            word[:1].upper() + word[1:] for word in suffix.split("-")
        )
        if f"function {expected}(" not in main_js.read_text(encoding="utf-8"):
            missing.append(f"{main_js.relative_to(ROOT)} -> {expected}")

    assert missing == []
