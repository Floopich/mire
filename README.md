<p align="center"><img src="internal/webview/assets/mire.svg" width="96" alt="Logo de Mire"></p>

<h1 align="center">Mire</h1>

<p align="center">Surveillance continue d'une ligne câble DOCSIS : chaque relevé du modem conservé, l'état de la ligne dit en une phrase.</p>

![Vue d'ensemble de Mire](docs/images/vue-ensemble.png)

Un modem câble mesure en permanence la qualité de la ligne, mais n'en garde rien : son interface n'affiche qu'un instantané. Or une ligne se dégrade peu à peu, et l'on n'en voit que les symptômes : la visio qui se fige, le débit qui chute le soir. Mire relève ces mesures, les conserve et les juge selon les seuils de l'opérateur : ce qui se dégrade, depuis quand, et pourquoi.

> **In English.** Mire continuously monitors a DOCSIS cable line: it polls the modem, keeps every reading and judges each channel against the operator's thresholds. Internet probe, throughput from Speedtest Tracker or a UniFi gateway, line report with CSV export, notifications, Prometheus metrics and a Home Assistant device. Single binary or Docker image; the interface is in French. Profiles for the VOO and Telenet networks (Belgium), driver for the Technicolor CGA4233; other modems and operators are welcome, see [CONTRIBUTING.md](CONTRIBUTING.md).

## Fonctionnalités

- **État de la ligne** : chaque canal jugé selon les seuils de l'opérateur (bon, toléré, à surveiller, critique), avec la cause en une phrase.
- **Journal** : changements d'état, redémarrages du modem, canaux perdus, pics d'erreurs, sauts de puissance, et vos notes d'intervention.
- **Internet** : une sonde date chaque coupure et distingue la ligne du réseau local ; latence heure par heure, débit lu dans Speedtest Tracker ou sur une passerelle UniFi.
- **Tendance** : l'évolution sur 30 jours à un an, rapprochée de la température extérieure.
- **Rapport** : le bilan d'une période, à imprimer en PDF, avec la comparaison avant et après une intervention ; les données s'exportent en CSV.
- **Modem** : informations, réseau domestique en mode routeur, redémarrage.
- **Notifications** : ntfy, Gotify, Discord, Telegram, Slack, mail ou webhook.
- **Intégrations** : métriques Prometheus, appareil Home Assistant par MQTT.
- **Simple** : un binaire ou une image Docker, une base SQLite, aucun service externe ; installable comme une application sur téléphone.

## Aperçu

| | |
|---|---|
| ![Page Internet](docs/images/internet.png) | ![Tendance sur 90 jours](docs/images/tendance.png) |
| ![Rapport, avant et après une intervention](docs/images/rapport.png) | ![Journal des événements](docs/images/evenements.png) |

## Installation

Enregistrez ce `compose.yaml`, puis lancez `docker compose up -d` :

```yaml
services:
  mire:
    image: ghcr.io/floopich/mire:latest
    container_name: mire
    restart: unless-stopped
    ports:
      - "1340:1340"
    volumes:
      - mire-data:/data

volumes:
  mire-data:
```

Ouvrez `http://<machine>:1340`. L'accueil propose d'**essayer la démo**, sur une ligne fictive, ou de **configurer Mire** en trois étapes : mot de passe, opérateur, modem, que Mire cherche lui-même.

- **Mettre à jour** : `docker compose pull && docker compose up -d`.
- **Démo seule**, sans rien conserver : `docker run --rm -p 1340:1340 -e MIRE_DEMO=true ghcr.io/floopich/mire:latest`.
- **Sans Docker** : binaires pour Linux (amd64, arm64, armv7) et Windows dans les [Releases](https://github.com/floopich/mire/releases), service systemd décrit dans la [configuration](docs/configuration.md#installation-sans-docker).

## Modems et opérateurs

| Modem | Opérateur |
|---|---|
| Technicolor CGA4233 (firmware VOO) | VOO |

| Profil | Réseau |
|---|---|
| VOO / Orange | réseau VOO : Wallonie, une partie de Bruxelles |
| Telenet / Orange | réseau Telenet : Flandre, une partie de Bruxelles |
| Non calibré | autre opérateur : les relevés sont conservés sans juger la ligne |

Votre modem n'y figure pas ? Choisissez « Autre modem » : la sonde Internet fonctionne, et la page Modem prépare en quelques clics le rapport anonymisé qui permet de l'ajouter.

## Documentation

- [Fonctionnement](docs/fonctionnement.md) : comment Mire juge la ligne, journal, sonde Internet, tendance, rapport.
- [Configuration](docs/configuration.md) : réglages, variables d'environnement, commandes, sauvegarde, sécurité.
- [Intégrations](docs/integrations.md) : débit, notifications, métriques, Home Assistant, supervision.
- [Contribuer](CONTRIBUTING.md) : ajouter un modem ou un opérateur ; [charte graphique](docs/charte.md).

## Licence

MIT, copyright (c) 2026 Floopich : voir [LICENSE](LICENSE). Ressources embarquées : Roboto Flex (SIL Open Font License 1.1) et Material Symbols (Apache License 2.0), licences dans `internal/webview/assets/`.
