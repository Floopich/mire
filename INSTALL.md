# Installation Guide

Ce document couvre Docker Run, Docker Compose, Portainer, Synology NAS,
Unraid, la mise a jour et le depannage.

## Windows

On Windows 10/11, install Docker Desktop and follow the Quick Start below: it is the
only supported path for continuous collection. A portable Desktop Preview build is
planned but no release has been published yet.

If setup or collection does not behave as expected, run the passive local doctor inside the same container before collecting manual environment details:

```bash
docker exec mire python -m app.doctor
docker exec mire python -m app.doctor --json > mire-doctor.json
```

The default doctor checks local runtime, config, storage, database, secret-file presence, and optional integration configuration without contacting third-party services.

Optional alert fan-out through an Apprise sidecar is covered in [docs/notifications-apprise.md](docs/notifications-apprise.md).

## Quick Start

```bash
docker run -d \
  --name mire \
  --restart unless-stopped \
  -p 1340:1340 \
  -v mire_data:/data \
  ghcr.io/floopich/mire:latest
```

Open `http://localhost:1340` and follow the setup wizard.

## Bare-Metal / systemd

If you run Mire outside of Docker (e.g. as a systemd service), you need to compile and install the native helpers manually. These are tiny C programs that need setuid root because ICMP raw sockets require elevated privileges.

```bash
# Install build dependencies (Debian/Ubuntu)
sudo apt install gcc libffi-dev libjpeg62-turbo-dev zlib1g-dev

# Install Python dependencies
pip install -r requirements.txt

# Compile and install the ICMP helpers
sudo gcc -O2 -Wall -o /usr/local/bin/mire-icmp-helper tools/icmp_probe_helper.c
sudo gcc -O2 -Wall -o /usr/local/bin/mire-traceroute-helper tools/traceroute_helper.c

# Set ownership and setuid bit
sudo chown root:root /usr/local/bin/mire-icmp-helper /usr/local/bin/mire-traceroute-helper
sudo chmod 4755 /usr/local/bin/mire-icmp-helper /usr/local/bin/mire-traceroute-helper
```

Without these binaries, traceroute and ICMP probes will log errors but the rest of Mire works fine.

## Reverse Proxy

For HTTPS, forwarded client/protocol headers, or an external path prefix such as
`/mire`, see the [reverse-proxy guide](docs/reverse-proxy.md). It covers the path-prefix
setup plus Caddy, Nginx, and Traefik examples.
