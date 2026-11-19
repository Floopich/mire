# syntax=docker/dockerfile:1

# Compilation croisee depuis l'architecture de la machine de build : le
# binaire est en Go pur (-tags purego), aucune chaine C n'est necessaire.
# Images de base figees par empreinte : la construction est reproductible,
# et Dependabot propose chaque mise a jour.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build
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
FROM gcr.io/distroless/static-debian13:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931
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
