#!/usr/bin/env bash
# Applique le delta Mire sur un clone de Mire.
# A relancer apres chaque merge de l'amont.
set -euo pipefail
cd "$(dirname "$0")/.."

test -f app/drivers/registry.py || { echo "Pas a la racine du depot"; exit 1; }
test -f app/drivers/voo_cga4233.py || { echo "app/drivers/voo_cga4233.py manquant"; exit 1; }

# 1. Drivers : ne garder que voo_cga4233 et generic
KEEP="__init__.py base.py registry.py generic.py utils.py format_compat.py formats voo_cga4233.py"
for f in app/drivers/*; do
  b="$(basename "$f")"
  case " $KEEP " in *" $b "*) continue ;; esac
  rm -rf "$f"
done

cat > app/drivers/__init__.py << 'PY'
"""Modem driver abstractions."""

from .registry import DriverRegistry

driver_registry = DriverRegistry()

driver_registry.register_builtin(
    "voo_cga4233",
    "app.drivers.voo_cga4233.VooCGA4233Driver",
    "voo_cga4233",
    hints={"default_url": "http://192.168.100.1", "default_user": "admin"},
)
driver_registry.register_builtin(
    "generic",
    "app.drivers.generic.GenericDriver",
    "Generic Router (No DOCSIS)",
    hints={"credentials_required": False},
)


def load_driver(modem_type, url, user, password):
    """Backward-compatible wrapper around driver_registry.load_driver()."""
    return driver_registry.load_driver(modem_type, url, user, password)
PY

sed -i 's/"modem_type": "fritzbox"/"modem_type": "generic"/' app/config.py
sed -i 's/get("modem_type", "fritzbox")/get("modem_type", "generic")/' app/main.py app/collectors/__init__.py

# 2. Tests des drivers et modules supprimes
rm -rf tests/drivers/surfboard tests/fixtures/sb6183
rm -f tests/test_arris_html.py \
      tests/test_cgm4981_driver.py \
      tests/test_cm3000_driver.py \
      tests/test_cm8200_driver.py \
      tests/test_hitron_coda_4680_driver.py \
      tests/test_hitron_driver.py \
      tests/test_sagemcom_driver.py \
      tests/test_surfboard_html_fallback.py \
      tests/test_ultrahub7_auth.py \
      tests/test_vodafone_station_cga.py \
      tests/test_vodafone_station_tg.py \
      tests/test_fritzbox_api.py \
      tests/test_correlation_segment.py \
      tests/modules/test_fritzbox_cable_collector.py \
      tests/modules/test_fritzbox_cable_routes.py \
      tests/modules/test_fritzbox_cable_storage.py \
      tests/e2e/test_segment_utilization.py

# 3. Module BNetzA : importeur du CSV de l'outil de mesure officiel allemand,
#    sans equivalent belge. Les references externes sont toutes sous try/except
#    ou en parametre optionnel, la suppression degrade proprement.
rm -rf app/modules/bnetz
rm -f tests/modules/test_bnetz*.py tests/test_bnetz*.py

# 4. Langues : ne garder que les langues officielles belges + anglais
python3 - << 'PYLANG'
from pathlib import Path
KEEP = {"fr", "nl", "de", "en"}
for d in ("app/i18n", "app/modules/reports/i18n", "app/modules/modulation/i18n"):
    p = Path(d)
    if not p.is_dir():
        continue
    for f in p.glob("*.json"):
        if f.stem not in KEEP and f.name != "template.json":
            f.unlink()
PYLANG

# 5. Fonction FRITZ!Box (utilisation de segment) : sans objet ici
rm -f app/fritzbox.py app/collectors/segment_utilization.py app/blueprints/segment_bp.py

python3 - << 'PY'
import re
from pathlib import Path

def edit(path, fn):
    p = Path(path)
    s = p.read_text()
    out = fn(s)
    if out == s:
        raise SystemExit(f"motif introuvable dans {path} — l'amont a change, script a revoir")
    p.write_text(out)

edit("app/blueprints/__init__.py",
     lambda s: s.replace("    from .segment_bp import segment_bp\n", "")
                .replace("        segment_bp,\n", ""))

edit("app/collectors/__init__.py", lambda s: re.sub(
    r"\n *# Segment utilization collector \(FritzBox only\)\n"
    r"(?: *if modem_type == \"fritzbox\":\n)"
    r"(?:(?: {8,}.*)?\n)*?"
    r" *collectors\.append\(segment_collector\)\n", "\n", s, count=1))

edit("app/web.py", lambda s: re.sub(
    r"\n *\{\n *\"id\": \"core\.segment_utilization\",\n(?:.*\n)*? *\},\n", "\n", s, count=1))

edit("app/web.py", lambda s: re.sub(
    r"is_fritzbox = \(?_config_manager\.get\(\"modem_type\"\).*", "is_fritzbox = False", s)
    if "is_fritzbox = (" in s else s)

edit("app/config.py",
     lambda s: s.replace('"segment_utilization_enabled": True,',
                         '"segment_utilization_enabled": False,'))
PY

sed -i 's/is_fritzbox = config.get("modem_type") == "fritzbox"/is_fritzbox = False/' app/web.py

python3 -c "
import ast,glob
for f in glob.glob('app/**/*.py', recursive=True):
    ast.parse(open(f).read(), f)
print('syntaxe ok sur', len(glob.glob('app/**/*.py', recursive=True)), 'fichiers')
"
! grep -rn "from .segment_utilization\|from .segment_bp\|from app import fritzbox\|app\.fritzbox" --include='*.py' app/ || { echo "reference orpheline restante"; exit 1; }
echo "Drivers restants : $(ls app/drivers | tr '\n' ' ')"
