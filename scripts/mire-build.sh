#!/usr/bin/env bash
# Construit l'image Mire de facon identique sur toute machine Docker.
#   ./scripts/mire-build.sh                  -> mire:local, architecture de l'hote
#   ./scripts/mire-build.sh 2026.08          -> mire:2026.08
#   PLATFORM=linux/arm64 ./scripts/mire-build.sh  -> build croise (buildx requis)
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${1:-local}"
IMAGE="mire:${VERSION}"
PLATFORM="${PLATFORM:-}"

echo "== Verification de la base multi-architecture =="
BASE="$(awk '/^FROM python/{print $2; exit}' Dockerfile)"
docker manifest inspect "$BASE" >/dev/null 2>&1 \
  && echo "base ok : $BASE" \
  || echo "ATTENTION : $BASE n'expose pas de manifeste multi-arch lisible ici"

if [ -n "$PLATFORM" ]; then
  docker buildx build --platform "$PLATFORM" --build-arg "VERSION=$VERSION" \
    -t "$IMAGE" --load .
else
  docker build --build-arg "VERSION=$VERSION" -t "$IMAGE" .
fi

echo "== Image construite =="
docker image inspect "$IMAGE" --format '{{.Id}} {{.Architecture}} {{.Os}} {{index .Config.Labels "org.opencontainers.image.version"}}'
echo
echo "Transfert vers une autre machine :"
echo "  docker save $IMAGE | ssh <hote> 'sudo docker load'"
echo "Puis sur l'hote cible : MIRE_IMAGE=$IMAGE docker compose up -d"
