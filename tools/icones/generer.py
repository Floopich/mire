"""Genere les icones PNG de l'application installable a partir des SVG.

    pip install playwright && playwright install chromium
    python3 tools/icones/generer.py

Le rendu passe par Chromium, celui des navigateurs qui afficheront l'icone.
"""
import pathlib
from playwright.sync_api import sync_playwright

ici = pathlib.Path(__file__).resolve().parent
sortie = ici.parents[1] / "internal" / "webview" / "assets"
icones = [
    ("icone.svg", "icone-192.png", 192),
    ("icone.svg", "icone-512.png", 512),
    ("icone-masquable.svg", "icone-masquable-512.png", 512),
    # iOS arrondit lui-meme les coins : fond plein, comme l'icone masquable.
    ("icone-masquable.svg", "icone-apple-180.png", 180),
]

with sync_playwright() as p:
    navigateur = p.chromium.launch()
    for source, cible, taille in icones:
        page = navigateur.new_page(viewport={"width": taille, "height": taille})
        svg = (ici / source).read_text()
        page.set_content(
            f'<html><body style="margin:0;background:transparent">'
            f'<div style="width:{taille}px;height:{taille}px">{svg}</div></body></html>'
        )
        page.locator("svg").evaluate("e => { e.setAttribute('width', '100%'); e.setAttribute('height', '100%'); }")
        page.screenshot(path=str(sortie / cible), omit_background=True,
                        clip={"x": 0, "y": 0, "width": taille, "height": taille})
        page.close()
        print(cible)
    navigateur.close()
