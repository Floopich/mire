"""Genere les icones PNG de l'application installable, une serie par style.

    pip install playwright && playwright install chromium
    python3 tools/icones/generer.py

Le logo ne change jamais : seules ses couleurs varient d'un style a
l'autre. Le rendu passe par Chromium, celui des navigateurs qui
afficheront l'icone. MIRE_CHROME designe un Chromium deja installe.
"""
import os
import pathlib
from playwright.sync_api import sync_playwright

ici = pathlib.Path(__file__).resolve().parent
sortie = ici.parents[1] / "internal" / "webview" / "assets"

# Styles : fond de la tuile, barres, barre centrale et ligne de seuil.
# Le style classique garde les noms historiques (icone-192.png...).
styles = {
    "classique": ("#FFF8F7", "#808080", "#E8817F"),
    "sombre": ("#201A19", "#A8A8A8", "#E8817F"),
    "inversee": ("#E8817F", "#FFFFFF", "#5C1D1C"),
    "noir-blanc": ("#FFFFFF", "#6E6E6E", "#000000"),
}

# Le logo, dans un repere de 128 sur 128.
logo = """
    <rect x="14" y="28" width="16" height="76" fill="{barres}"/>
    <rect x="35" y="46" width="16" height="58" fill="{barres}"/>
    <rect x="56" y="70" width="16" height="34" fill="{accent}"/>
    <rect x="77" y="46" width="16" height="58" fill="{barres}"/>
    <rect x="98" y="28" width="16" height="76" fill="{barres}"/>
    <line x1="4" y1="58" x2="124" y2="58" stroke="{accent}" stroke-width="4" stroke-dasharray="9 7" stroke-linecap="round" opacity="0.85"/>"""


def svg(fond, barres, accent, forme):
    dessin = logo.format(barres=barres, accent=accent)
    if forme == "tuile":  # « any » : tuile arrondie
        return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512">'
                f'<rect width="512" height="512" rx="112" fill="{fond}"/>'
                f'<g transform="translate(80 80) scale(2.75)">{dessin}</g></svg>')
    if forme == "plein":  # « maskable » et iOS : fond plein, logo dans la zone sure
        return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512">'
                f'<rect width="512" height="512" fill="{fond}"/>'
                f'<g transform="translate(108.8 108.8) scale(2.3)">{dessin}</g></svg>')
    # « monochrome » : silhouette blanche sur fond transparent, que le systeme
    # teinte (icones a theme d'Android).
    dessin = logo.format(barres="#FFFFFF", accent="#FFFFFF")
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512">'
            f'<g transform="translate(108.8 108.8) scale(2.3)">{dessin}</g></svg>')


def nom(style, suffixe):
    return f"icone-{suffixe}.png" if style == "classique" else f"icone-{style}-{suffixe}.png"


travaux = [("icone-monochrome-512.png", svg(None, None, None, "monochrome"), 512)]
for style, (fond, barres, accent) in styles.items():
    travaux += [
        (nom(style, "192"), svg(fond, barres, accent, "tuile"), 192),
        (nom(style, "512"), svg(fond, barres, accent, "tuile"), 512),
        (nom(style, "masquable-512"), svg(fond, barres, accent, "plein"), 512),
        # iOS arrondit lui-meme les coins : fond plein, comme l'icone masquable.
        (nom(style, "apple-180"), svg(fond, barres, accent, "plein"), 180),
    ]

with sync_playwright() as p:
    chrome = os.environ.get("MIRE_CHROME")
    navigateur = p.chromium.launch(executable_path=chrome) if chrome else p.chromium.launch()
    for cible, source, taille in travaux:
        page = navigateur.new_page(viewport={"width": taille, "height": taille})
        page.set_content(
            f'<html><body style="margin:0;background:transparent">'
            f'<div style="width:{taille}px;height:{taille}px">{source}</div></body></html>'
        )
        page.locator("svg").evaluate("e => { e.setAttribute('width', '100%'); e.setAttribute('height', '100%'); }")
        page.screenshot(path=str(sortie / cible), omit_background=True,
                        clip={"x": 0, "y": 0, "width": taille, "height": taille})
        page.close()
        print(cible)
    navigateur.close()
