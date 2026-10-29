# syntax=docker/dockerfile:1

# Compilation croisee depuis l'architecture de la machine de build : le
# binaire est en Go pur (-tags purego), aucune chaine C n'est necessaire.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH TARGETVARIANT
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOARM=${TARGETVARIANT#v} \
    go build -trimpath -tags purego -ldflags "-s -w -X main.version=$VERSION" \
    -o /out/mire ./cmd/mire \
 && mkdir -p /out/data

# Image minimale : ni shell ni gestionnaire de paquets, utilisateur non root.
FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/mire /usr/local/bin/mire
COPY --from=build --chown=65532:65532 /out/data /data
ENV MIRE_DONNEES=/data \
    MIRE_ECOUTE=0.0.0.0:1340
VOLUME ["/data"]
EXPOSE 1340
USER nonroot:nonroot
HEALTHCHECK --interval=1m --timeout=10s --start-period=30s --retries=3 \
  CMD ["/usr/local/bin/mire", "sante"]
ENTRYPOINT ["/usr/local/bin/mire"]
