# Sécurité

## Signaler une faille

Pas d'issue publique : onglet **Security**, **Report a vulnerability**. Indiquez la version (`mire version`), le comportement constaté et, si possible, comment le reproduire.

## Versions suivies

Seule la dernière version reçoit les corrections : `docker compose pull && docker compose up -d`, ou remplacez le binaire.

## Modèle de menace

Mire est prévu pour un réseau local, derrière un pare-feu.

**Protégé**
- L'interface : mot de passe choisi à la première visite, conservé sous forme d'empreinte ; sessions de 30 jours ; tentatives de connexion ralenties ; formulaires refusés depuis une autre origine, et au-delà de 1 Mo ; aucun script ni ressource externe ; pages ni intégrables ni lisibles depuis un autre site.
- Les secrets (mot de passe du modem, jetons, mots de passe MQTT et mail) : jamais réaffichés, absents du journal, des erreurs, des métriques et du rapport du modem. Une valeur effacée ne reste pas dans le fichier de la base.
- Le conteneur : système de fichiers en lecture seule, aucun privilège, utilisateur non root, image sans shell.
- Les connexions sortantes : seulement ce que vous activez (notifications, Home Assistant, météo, débit, avis de nouvelle version) et la sonde Internet vers ses cibles.

**Non protégé**
- La base `mire.db` et les sauvegardes complètes contiennent les secrets saisis dans les Réglages, en clair : quiconque lit le dossier de données les lit. Protégez ce dossier, ou utilisez une sauvegarde « sans mots de passe ni jetons ». Un secret passé par variable ou par fichier (`…_FICHIER`, voir [configuration](docs/configuration.md)) n'est pas écrit dans la base.
- `/sante` répond sans session (état de la collecte, modèle du modem, version) ; `/metrics` aussi, sauf si un jeton est défini.
- Le trafic entre le navigateur et Mire n'est chiffré que derrière un proxy inverse en HTTPS.

**À éviter** : exposer Mire directement sur Internet. Passez par un VPN, ou par un proxy inverse avec authentification.

## Vérifier une image ou un binaire

Chaque image et chaque binaire publié porte une attestation de provenance : elle prouve qu'il a été construit par ce dépôt, depuis son code publié. Avec [GitHub CLI](https://cli.github.com) connecté :

```bash
gh attestation verify oci://ghcr.io/floopich/mire:latest -R floopich/mire
gh attestation verify mire-linux-amd64 -R floopich/mire
```

## Liste des composants

Chaque image porte la liste des bibliothèques qu'elle contient (SBOM), lue par les scanners de failles. Chaque binaire contient la sienne, comme tout programme Go :

```bash
docker buildx imagetools inspect ghcr.io/floopich/mire:latest --format '{{ json .SBOM }}'
go version -m mire-linux-amd64
```
