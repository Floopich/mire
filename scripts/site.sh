#!/usr/bin/env bash
# Gestion des campagnes de mesure, un dossier de donnees par site.
set -euo pipefail
cd "$(dirname "$0")/.."

usage() {
  echo "usage: $0 {start|stop|archive|list} [site]"
  echo "  start : BOOKED_DOWNLOAD et BOOKED_UPLOAD (Mbit/s) peuvent etre passes en variables"
  echo "          ex. BOOKED_DOWNLOAD=1000 BOOKED_UPLOAD=50 $0 start dupont"
  exit 1
}
[ $# -ge 1 ] || usage
action="$1"
site="${2:-}"

owner() {
  # Reutilise la valeur deja dans .env si elle y est.
  if [ -f .env ] && grep -q '^GH_OWNER=.\+' .env; then
    sed -n 's/^GH_OWNER=//p' .env | head -1
    return
  fi
  url="$(git remote get-url origin 2>/dev/null || true)"
  [ -n "$url" ] || { echo "Pas de remote git origin : renseigner GH_OWNER dans .env" >&2; exit 1; }
  printf '%s' "$url" \
    | sed -E 's#^(https?://[^/]+/|git@[^:]+:|ssh://git@[^/]+/)([^/]+)/.*#\2#' \
    | tr '[:upper:]' '[:lower:]'
}

case "$action" in
  start)
    [ -n "$site" ] || usage
    mkdir -p "sites/$site/data"
    printf 'GH_OWNER=%s\nSITE=%s\nTZ=%s\nBOOKED_DOWNLOAD=%s\nBOOKED_UPLOAD=%s\nMIRE_TAG=%s\n' \
      "$(owner)" "$site" "${TZ:-Europe/Brussels}" \
      "${BOOKED_DOWNLOAD:-}" "${BOOKED_UPLOAD:-}" "${MIRE_TAG:-latest}" > .env
    if [ -z "${BOOKED_DOWNLOAD:-}" ] || [ -z "${BOOKED_UPLOAD:-}" ]; then
      echo "Debits souscrits non renseignes : le rapport affichera N/A." >&2
    fi
    docker compose up -d
    ip="$(hostname -I | awk '{print $1}')"
    echo "Campagne demarree pour $site"
    echo "Interface : http://$ip:${WEB_PORT:-1340}"
    echo "Verifier l'heure : $(date -Is)"
    ;;
  stop)
    docker compose down
    ;;
  archive)
    [ -n "$site" ] || usage
    docker compose down 2>/dev/null || true
    out="sites/${site}-$(date +%Y%m%d).tar.gz"
    tar czf "$out" -C sites "$site"
    echo "$out"
    ;;
  list)
    ls -1 sites/ 2>/dev/null || echo "aucune campagne"
    ;;
  *) usage ;;
esac
