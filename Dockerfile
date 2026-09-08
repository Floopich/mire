# --- builder stage: compile native dependencies ---
FROM python:3.13-slim@sha256:739e7213785e88c0f702dcdc12c0973afcbd606dbf021a589cab77d6b00b579d AS builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc \
    libffi-dev \
    libjpeg62-turbo-dev \
    zlib1g-dev \
    && rm -rf /var/lib/apt/lists/*

COPY requirements.txt .
RUN pip install --no-cache-dir --require-hashes --prefix=/install -r requirements.txt
COPY tools/icmp_probe_helper.c /build/icmp_probe_helper.c
COPY tools/traceroute_helper.c /build/traceroute_helper.c
RUN mkdir -p /build/out && \
    gcc -O2 -Wall -o /build/out/mire-icmp-helper /build/icmp_probe_helper.c && \
    gcc -O2 -Wall -o /build/out/mire-traceroute-helper /build/traceroute_helper.c

# --- runtime stage: slim final image ---
FROM python:3.13-slim@sha256:739e7213785e88c0f702dcdc12c0973afcbd606dbf021a589cab77d6b00b579d
ARG VERSION=dev
LABEL org.opencontainers.image.title="Mire" \
      org.opencontainers.image.description="Suivi local du signal DOCSIS, derive de Mire" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.base.name="docker.io/library/python:3.13-slim"
WORKDIR /app
RUN echo "${VERSION}" > /app/VERSION

COPY --from=builder /install /usr/local
COPY --from=builder /build/out/mire-icmp-helper /usr/local/bin/mire-icmp-helper
COPY --from=builder /build/out/mire-traceroute-helper /usr/local/bin/mire-traceroute-helper

# Les deux helpers sont setuid root : ils ouvrent leur socket raw puis
# larguent definitivement leurs privileges (setresuid) avant toute
# resolution de noms. Voir tools/*.c.
RUN apt-get update && apt-get install -y --no-install-recommends \
    gosu \
    libjpeg62-turbo \
    && chown root:root /usr/local/bin/mire-icmp-helper \
    && chmod 4755 /usr/local/bin/mire-icmp-helper \
    && chown root:root /usr/local/bin/mire-traceroute-helper \
    && chmod 4755 /usr/local/bin/mire-traceroute-helper \
    && rm -rf /var/lib/apt/lists/*

RUN adduser --disabled-password --gecos "" --uid 1000 appuser && \
    mkdir -p /data/modules /modules && \
    chown -R appuser:appuser /data /modules
COPY app/ ./app/
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
HEALTHCHECK --interval=60s --timeout=5s --retries=3 \
    CMD ["python", "-m", "app.healthcheck"]
ENTRYPOINT ["/entrypoint.sh"]
CMD ["python", "-m", "app.main"]
