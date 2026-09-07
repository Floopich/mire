# Installation Guide

👉 **See the [full installation guide](https://github.com/floopich/mire/wiki/Installation) in the wiki.**

Covers Docker Run, Docker Compose, Portainer, Synology NAS, Unraid, updating, and troubleshooting.

## Windows chooser

| If you want to... | Start here |
|---|---|
| Quickly try Mire on a Windows PC without Docker | [Download the portable Desktop Preview](https://github.com/floopich/mire/releases/latest), then read the [usage notes](docs/windows-desktop-preview.md) |
| Monitor your connection continuously on Windows | [Windows Docker Desktop quick start](docs/windows-quick-start.md) |

On Windows 10/11, the normal 24/7 monitoring path is Docker Desktop. Start with the [Windows quick start](docs/windows-quick-start.md) when you want the supported Docker path. The unsigned Desktop Preview is a portable tryout build published through GitHub Releases and is not intended for always-on collection.

If setup or collection does not behave as expected, run the passive local doctor inside the same container before collecting manual environment details:

```bash
docker exec mire python -m app.doctor
docker exec mire python -m app.doctor --json > mire-doctor.json
```

The default doctor checks local runtime, config, storage, database, secret-file presence, and optional integration configuration without contacting third-party services.

Optional alert fan-out through an Apprise sidecar is covered in [docs/notifications-apprise.md](docs/notifications-apprise.md). Optional browser/app push alerts are covered in [docs/notifications-pwa-web-push.md](docs/notifications-pwa-web-push.md).

## Quick Start

```bash
docker run -d \
  --name mire \
  --restart unless-stopped \
  -p 8765:8765 \
  -v mire_data:/data \
  ghcr.io/floopich/mire:latest
```

Open `http://localhost:8765` and follow the setup wizard.

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
`/mire`, start with the in-repository [path-prefix reverse-proxy guide](docs/reverse-proxy.md).
The broader [reverse proxy wiki guide](https://github.com/floopich/mire/wiki/Reverse-Proxy)
covers additional Caddy, Nginx, and Traefik deployment examples.
