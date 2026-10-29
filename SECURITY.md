# Sécurité

## Signaler une faille

Pas d'issue publique : onglet **Security**, **Report a vulnerability**. Indiquez la version (`mire version`), le comportement constaté et, si possible, comment le reproduire.

## Versions suivies

Seule la dernière version reçoit les corrections : `docker compose pull && docker compose up -d`, ou remplacez le binaire.

## Modèle de menace

Mire est prévu pour un réseau local, derrière un pare-feu.

**Protégé**
- L'interface : mot de passe choisi à la première visite, conservé sous forme d'empreinte ; sessions de 30 jours ; tentatives de connexion ralenties ; formulaires refusés depuis une autre origine ; aucun script ni ressource externe.
- Les secrets (mot de passe du modem, jetons, mots de passe MQTT et mail) : jamais réaffichés, absents du journal, des erreurs, des métriques et du rapport du modem. Une valeur effacée ne reste pas dans le fichier de la base.
- Le conteneur : système de fichiers en lecture seule, aucun privilège, utilisateur non root, image sans shell.

**Non protégé**
- La base `mire.db` et les sauvegardes complètes contiennent les secrets en clair : quiconque lit le dossier de données les lit. Protégez ce dossier, ou utilisez une sauvegarde « sans mots de passe ni jetons ».
- `/sante` répond sans session (état de la collecte, modèle du modem, version) ; `/metrics` aussi, sauf si un jeton est défini.
- Le trafic entre le navigateur et Mire n'est chiffré que derrière un proxy inverse en HTTPS.

**À éviter** : exposer Mire directement sur Internet. Passez par un VPN, ou par un proxy inverse avec authentification.

Détail des mesures : [docs/configuration.md](docs/configuration.md#sécurité).
