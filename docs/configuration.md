# Configuration et exploitation

## Réglages

Chaque rubrique est résumée en une ligne. Un changement s'applique aussitôt, sans redémarrer.

| Rubrique | Contenu |
|---|---|
| Modem | modèle, adresse, identifiant et mot de passe ; « Enregistrer et tester », « Détecter le modem » |
| Collecte et conservation | intervalle entre deux relevés, relevés rapprochés après un incident, durée et taille de conservation |
| Opérateur et seuils | profil d'opérateur, modulation habituelle de la voie montante OFDMA |
| Abonnement | prix mensuel, pour la compensation légale (opérateur belge) |
| Internet | sonde de connexion : cibles et intervalle |
| Débit | source (Speedtest Tracker ou passerelle UniFi), adresse, jeton ou clé API, compte local UniFi, essai |
| Notifications | service, événements envoyés, essai |
| Météo | commune ou coordonnées, essai ; température pour la page Tendance |
| Métriques | publication sur `/metrics`, jeton d'accès |
| Home Assistant | serveur MQTT, identifiant, préfixe de découverte, bouton de redémarrage, essai |
| Journal | niveau de détail, dernières lignes du service, téléchargement |
| Sécurité, Sauvegarde | mot de passe de l'interface, copie de la base |

![Page Réglages](images/reglages.png)

## Variables d'environnement

Facultatives. Une variable définie **verrouille** le réglage dans la page. À placer dans `environment:`, ou dans un fichier `.env` déclaré par `env_file: .env` (modèle : [.env.exemple](../.env.exemple)).

| Variable | Défaut | Rôle |
|---|---|---|
| `MIRE_MODELE` | celui du profil | modèle du modem, voir `mire modeles` ; `generique` pour un modem non pris en charge |
| `MIRE_MODEM` | celle du profil | adresse du modem, `http://192.168.100.1` chez VOO |
| `MIRE_UTILISATEUR` | celui du modèle | identifiant du modem |
| `MIRE_MODEM_MOT_DE_PASSE` | — | mot de passe du modem ; sans lui, la collecte reste arrêtée |
| `MIRE_INTERVALLE` | `10m` | délai entre deux relevés, une minute au minimum |
| `MIRE_RAPPROCHE` | `true` | relevés toutes les minutes pendant le quart d'heure qui suit un incident |
| `MIRE_PROFIL` | `voo` | profil d'opérateur : `voo` (VOO, Orange sur VOO), `telenet` (Telenet, Orange sur Telenet) ou `generique` |
| `MIRE_OFDMA_PLANCHER` | celle du profil | modulation habituelle de la voie montante OFDMA, en ordre QAM : `16` pour 16QAM |
| `MIRE_ABONNEMENT` | — | prix mensuel de l'abonnement en euros, pour la compensation légale belge |
| `MIRE_RETENTION` | `3650` | conservation en jours, 0 pour illimitée |
| `MIRE_TAILLE_MAX` | `5` | plafond de la base en Go, 0 pour illimité |
| `MIRE_SONDE` | `true` | sonde Internet active ou non |
| `MIRE_SONDE_CIBLES` | `1.1.1.1:443 8.8.8.8:443 9.9.9.9:443` | cibles de la sonde, `hôte:port` séparés par des espaces, cinq au plus |
| `MIRE_SONDE_INTERVALLE` | `30s` | délai entre deux tentatives, de 10 secondes à 10 minutes |
| `MIRE_JOURNAL` | `normal` | niveau du journal : `erreurs`, `normal` ou `detaille` (une ligne par relevé) |

Notifications :

| Service | Variables |
|---|---|
| Commun | `MIRE_NOTIF` (`true` pour activer), `MIRE_NOTIF_SERVICE` (`ntfy`, `gotify`, `discord`, `telegram`, `slack`, `mail`, `webhook`), `MIRE_NOTIF_FILTRE` (`etat`, `critiques`, `tout`), `MIRE_NOTIF_ADRESSE` (adresse de Mire, pour le lien des messages) |
| ntfy | `MIRE_NTFY_SERVEUR` (`https://ntfy.sh` par défaut), `MIRE_NTFY_SUJET`, `MIRE_NTFY_JETON` |
| Gotify | `MIRE_GOTIFY_SERVEUR`, `MIRE_GOTIFY_JETON` |
| Discord | `MIRE_DISCORD_WEBHOOK` |
| Telegram | `MIRE_TELEGRAM_JETON`, `MIRE_TELEGRAM_CHAT` |
| Slack | `MIRE_SLACK_WEBHOOK` |
| Mail | `MIRE_MAIL_SERVEUR` (`serveur:port`), `MIRE_MAIL_SECURITE` (`starttls`, `tls`, `aucun`), `MIRE_MAIL_IDENTIFIANT`, `MIRE_MAIL_MOT_DE_PASSE`, `MIRE_MAIL_EXPEDITEUR`, `MIRE_MAIL_DESTINATAIRE` |
| Webhook | `MIRE_WEBHOOK_URL` |

Débit, météo, métriques et Home Assistant :

| Fonction | Variables |
|---|---|
| Débit | `MIRE_DEBIT` (`true` pour lire les mesures), `MIRE_DEBIT_SOURCE` (`speedtest-tracker` par défaut, ou `unifi`), `MIRE_DEBIT_ADRESSE`, `MIRE_DEBIT_JETON` (jeton ou clé API), `MIRE_DEBIT_IDENTIFIANT` et `MIRE_DEBIT_MOT_DE_PASSE` (compte local UniFi) |
| Météo | `MIRE_METEO` (`true` pour relever la température), `MIRE_METEO_POSITION` (commune, ou `latitude, longitude`) |
| Métriques | `MIRE_METRIQUES` (`true` pour publier), `MIRE_METRIQUES_JETON` |
| Home Assistant | `MIRE_MQTT` (`true` pour publier), `MIRE_MQTT_SERVEUR` (`mqtt://hôte:1883` ou `mqtts://hôte:8883`), `MIRE_MQTT_IDENTIFIANT`, `MIRE_MQTT_MOT_DE_PASSE`, `MIRE_MQTT_PREFIXE` (`homeassistant` par défaut), `MIRE_MQTT_REDEMARRAGE` (`true` par défaut : bouton de redémarrage du modem) |

Service, hors de la page Réglages :

| Variable | Défaut | Rôle |
|---|---|---|
| `TZ` | celui de l'opérateur | fuseau horaire des heures affichées, par exemple `Europe/Paris` |
| `MIRE_DONNEES` | `.` (`/data` dans l'image) | dossier de la base |
| `MIRE_ECOUTE` | `0.0.0.0:1340` | adresse d'écoute de l'interface |
| `MIRE_DEMO` | `false` | démonstration sur une ligne fictive : base jetable, interface sans mot de passe |

Chaque variable a son option en ligne de commande (`mire -h`), sauf le mot de passe du modem. Les données tiennent dans un fichier SQLite, `mire.db`.

## Docker

Image pour `amd64`, `arm64` et `armv7`.

| Tag de l'image | Contenu |
|---|---|
| `latest` | dernière version publiée |
| `1.2.3`, `1.2` | version précise, ou dernière correction d'une version |
| `main` | version en développement |

Mettre à jour : `docker compose pull && docker compose up -d`. Les données sont conservées et la base est mise à jour au démarrage ; avant de la modifier, Mire en garde une copie à côté, `mire.db.avant-vN` (la plus récente seulement).

**Dossier des données.** Pour un dossier sur la machine plutôt que le volume `mire-data`, remplacez `mire-data:/data` par `./data:/data` et attribuez-le à l'utilisateur du conteneur (65532) avant le premier démarrage :

```bash
mkdir data && sudo chown 65532:65532 data
```

Sinon, Mire ne démarre pas et `docker compose logs mire` indique la commande à lancer.

## Installation sans Docker

Binaires Linux (amd64, arm64, armv7) et Windows dans les [Releases](https://github.com/floopich/mire/releases). Pour un essai : `./mire -donnees ./donnees`, puis `http://<machine>:1340`.

Service systemd sous Linux, en root :

```bash
A=linux-amd64   # ou linux-arm64, linux-armv7
curl -fsSLO https://github.com/floopich/mire/releases/latest/download/mire-$A
curl -fsSLO https://github.com/floopich/mire/releases/latest/download/SHA256SUMS
sha256sum --check --ignore-missing SHA256SUMS
install -m 755 mire-$A /usr/local/bin/mire
useradd --system --home-dir /var/lib/mire --shell /usr/sbin/nologin mire
curl -fsSL -o /etc/systemd/system/mire.service https://raw.githubusercontent.com/floopich/mire/main/deploy/mire.service
systemctl daemon-reload && systemctl enable --now mire
```

Ouvrez ensuite `http://<machine>:1340`. Le service tourne sous le compte `mire` et n'écrit que dans `/var/lib/mire`. Réglages imposés, facultatifs : `/etc/mire/mire.env`, au format de [.env.exemple](../.env.exemple). Mise à jour : les quatre premières lignes, puis `install -m 755 mire-$A /usr/local/bin/mire && systemctl restart mire`. Journal : `journalctl -u mire`.

## Commandes

| Commande | Rôle |
|---|---|
| `mire` | collecte les relevés et sert l'interface |
| `mire releve` | prend un relevé et l'enregistre |
| `mire sauvegarde FICHIER` | copie la base, sans arrêter le service ; `-` pour la sortie standard ; `-sans-secrets` retire mots de passe et jetons |
| `mire restauration FICHIER` | remplace la base par une sauvegarde, service arrêté ; `-` pour l'entrée standard |
| `mire mot-de-passe` | change le mot de passe de l'interface et ferme les sessions |
| `mire sante` | vérifie qu'une instance répond |
| `mire detecter` | cherche le modem sur le réseau |
| `mire diagnostic` | rapport anonymisé du modem (`-modem http://adresse` pour un modèle inconnu) ; lecture seule |
| `mire modeles` | liste les modems pris en charge |
| `mire version` | affiche la version |

Avec Docker : `docker compose exec -it mire mire mot-de-passe`, ou `docker compose exec -T mire mire diagnostic > modem.txt`.

## Sauvegarde

Sans arrêter la collecte :

- dans l'interface : **Réglages, Sauvegarde**, « Sauvegarde complète » ou « Sans mots de passe ni jetons » ;
- en ligne de commande, par exemple chaque nuit :

```bash
# Docker
docker compose exec -T mire mire sauvegarde - > mire-$(date +%F).db
# Sans Docker
mire sauvegarde /chemin/mire-sauvegarde.db
```

La copie ne remplace jamais un fichier existant. Complète, elle contient le mot de passe du modem et les jetons : gardez-la à l'abri. Avec `-sans-secrets` (`mire sauvegarde -sans-secrets FICHIER`), mots de passe, jetons et sessions sont retirés ; après restauration, ils se ressaisissent, celui de l'interface compris.

**Restaurer**, Mire arrêté : l'ancienne base est conservée à côté, sous un nom daté.

```bash
# Docker
docker compose stop mire
docker compose run --rm -T mire restauration - < mire-2026-10-27.db
docker compose start mire
# Sans Docker, service arrêté
mire restauration /chemin/mire-sauvegarde.db
```

## Derrière un proxy inverse

Mire doit être servi à la racine d'un nom de domaine (`https://mire.exemple.be`). Le cookie de session passe en `Secure` quand le proxy transmet `X-Forwarded-Proto: https`.

## Installer comme une application

![Vue d'ensemble et page d'un canal sur téléphone, thème sombre](images/mobile-sombre.png)

- **Android, Chrome, Edge** : menu du navigateur, « Installer l'application ».
- **iPhone, iPad** : Safari, Partager, « Sur l'écran d'accueil ».

L'installation demande une adresse en HTTPS.

## Sécurité

- Aucun mot de passe par défaut : il est choisi à la première visite.
- Mot de passe de l'interface conservé sous forme d'empreinte. Celui du modem est enregistré dans la base, jamais réaffiché : protégez le dossier de données, ou passez-le par `MIRE_MODEM_MOT_DE_PASSE` dans un `.env` en `chmod 600`.
- Sessions de 30 jours ; cookie `HttpOnly` et `SameSite=Strict`.
- Formulaires refusés depuis une autre origine.
- Aucun script ni ressource externe.
- Image sans shell, utilisateur non root ; le `compose.yaml` fourni ajoute un système de fichiers en lecture seule, sans aucun privilège (`read_only`, `cap_drop: ALL`, `no-new-privileges`).
- Valeur supprimée effacée du fichier de la base (`secure_delete`).
- Failles connues des dépendances vérifiées à chaque modification et chaque semaine (`govulncheck`).
- Ne sort du réseau local que ce que vous activez (notifications, MQTT, météo, débit) et les connexions de la sonde vers ses cibles.
