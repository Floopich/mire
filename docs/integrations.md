# Intégrations

Chaque intégration s'active dans **Réglages**, se vérifie avec « Enregistrer et tester », et accepte aussi ses variables d'environnement ([configuration](configuration.md#variables-denvironnement)).

## Débit

Mire ne mesure pas le débit lui-même : un test saturerait la ligne et fausserait la sonde. Il lit, tous les quarts d'heure, les mesures faites ailleurs : débits descendant et montant dans le temps, médianes et plus bas de la période. Le rapport les reprend, et compare le débit avant et après une intervention. À activer dans **Réglages, Débit**, avec l'une de ces deux sources :

- [Speedtest Tracker](https://github.com/alexjustesen/speedtest-tracker), par son API et un jeton créé au menu API Tokens, avec le seul droit « Read results » ; tout son historique est repris.
- une **passerelle UniFi** (Cloud Gateway, Dream Machine), qui fait ses propres tests de débit, à planifier dans UniFi Network. Depuis UniFi Network 11, elle ne rend que le dernier test : Mire le relève à chaque lecture et construit l'historique à partir de l'activation. Accès par une clé API de UniFi Network (Paramètres, Intégrations), ou par un compte local de la console, sans double authentification, de préférence en lecture seule. Le certificat autosigné de la passerelle n'est accepté que sur une adresse du réseau local. Avec un accès de secours (4G), seuls les tests de l'accès principal comptent.

## Notifications

![Réglages des notifications](images/notifications.png)

Mire envoie les événements du journal vers le service de votre choix : **ntfy**, **Gotify**, **Discord**, **Telegram**, **Slack**, **mail** ou un **webhook**. Dans les réglages, un interrupteur active l'option, puis une liste choisit le service ; seuls les champs de ce service s'affichent. « Enregistrer et envoyer un essai » vérifie la configuration, avant même d'activer l'option.

| Événements envoyés | Contenu |
|---|---|
| État de la ligne et coupures (par défaut) | ligne dégradée ou rétablie, coupure d'Internet, modem injoignable, et leur fin |
| Alertes critiques seulement | ce qui est critique, quel que soit le type |
| Tout le journal | chaque événement, y compris modulation, canaux et pics d'erreurs |

- Les événements proches partent ensemble, en un seul message.
- Un envoi qui échoue est retenté tant que l'événement a moins d'une heure ; au-delà, il n'avertirait plus de rien.
- À l'activation, le passé n'est pas envoyé.
- Avec l'adresse de Mire renseignée, chaque message mène au journal.
- Le webhook reçoit un JSON : `titre`, `message`, `niveau` (`bon`, `avertissement`, `critique`, `information`), `evenements`, `heure`, `lien`.

## Métriques

Les métriques sont publiées sur `GET /metrics` au format texte de Prometheus : Prometheus, VictoriaMetrics, Grafana Alloy ou Telegraf (pour InfluxDB) viennent les lire. Elles s'activent dans **Réglages, Métriques** (ou `MIRE_METRIQUES=true`), avec un jeton facultatif (`MIRE_METRIQUES_JETON`, à créer avec `openssl rand -hex 24`). Le point ne demande pas de session ; un jeton, s'il est défini, est exigé en `Authorization: Bearer`. Derrière un proxy inverse qui protège l'interface, faites lire le collecteur directement sur le port 1340.

| Métrique | Contenu |
|---|---|
| `mire_line_level` | état de la ligne : 0 non qualifié, 1 bon, 2 toléré, 3 à surveiller, 4 critique |
| `mire_channels_attention` | canaux à surveiller ou critiques |
| `mire_downstream_power_dbmv`, `mire_downstream_snr_db`, `mire_downstream_frequency_mhz` | par canal descendant (`channel`, `modulation`) |
| `mire_downstream_corrected_total`, `mire_downstream_uncorrected_total` | compteurs d'erreurs du modem, remis à zéro à son redémarrage : à lire avec `rate()` ou `increase()` |
| `mire_downstream_level`, `mire_upstream_level` | état par canal, même échelle que `mire_line_level` |
| `mire_upstream_power_dbmv`, `mire_upstream_frequency_mhz` | par canal montant |
| `mire_internet_up`, `mire_probe_latency_seconds`, `mire_probe_loss_ratio` | sonde Internet : connexion, latence et pertes de la dernière tranche |
| `mire_last_reading_timestamp_seconds`, `mire_info` | heure du dernier relevé ; version, modem et profil |
| `mire_modem_up` | 1 si le modem répond et que le dernier relevé est récent |
| `mire_outages{window="24h"\|"30d"}`, `mire_last_outage_start_timestamp_seconds`, `mire_last_outage_duration_seconds` | coupures d'Internet, hors réseau local |
| `mire_modem_last_restart_timestamp_seconds` | dernier redémarrage du modem constaté |

```yaml
# Prometheus, ou vmagent pour VictoriaMetrics
scrape_configs:
  - job_name: mire
    authorization: { credentials: "<jeton>" }   # si un jeton est défini
    static_configs:
      - targets: ["<machine>:1340"]
```

```alloy
// Grafana Alloy
prometheus.scrape "mire" {
  targets    = [{ __address__ = "<machine>:1340" }]
  forward_to = [prometheus.remote_write.default.receiver]
}
```

Un tableau de bord prêt à l'emploi accompagne le dépôt : [docs/grafana/mire.json](grafana/mire.json). Dans Grafana, **Dashboards**, **New**, **Import**, puis choisissez la source Prometheus ou VictoriaMetrics qui lit Mire. Il montre l'état de la ligne et d'Internet dans le temps, la puissance, le SNR et l'émission de chaque canal, les erreurs non corrigées par heure, la latence et les pertes. Ses repères de puissance sont ceux du profil VOO : adaptez-les à votre opérateur.

## Home Assistant

Mire publie sur un serveur MQTT (Mosquitto, par exemple) avec la découverte automatique : un appareil **Mire** apparaît dans l'intégration MQTT, sans configuration. La publication s'active dans **Réglages, Home Assistant** : serveur (`mqtt://192.168.1.10:1883`), identifiant et mot de passe, puis « Enregistrer et tester ». Avec le module Mosquitto de Home Assistant, un utilisateur Home Assistant sans droits d'administration suffit comme identifiant. Les mêmes réglages s'imposent aussi par variables (`MIRE_MQTT`, `MIRE_MQTT_SERVEUR`…) ; le journal du conteneur indique aussi `Home Assistant : connecté au serveur MQTT`.

Les **sondes** donnent un verdict, prêt pour un tableau de bord ou une automatisation ; les **mesures** donnent les chiffres.

| Capteur | Type | Contenu |
|---|---|---|
| Internet | sonde | connecté, ou déconnecté pendant une coupure constatée par la sonde |
| Signal | sonde | OK, ou problème quand la ligne est à surveiller ou critique |
| Modem | sonde | connecté tant que ses relevés arrivent ; déconnecté s'il ne répond plus |
| État de la ligne | sonde | Bon, Toléré, À surveiller, Critique ou Non qualifié |
| Diagnostic | sonde | la cause en une phrase, comme au journal : « Ligne critique : canal descendant 159, puissance descendante 26,6 dBmV, hors limites. » |
| Dernière coupure, Durée de la dernière coupure | coupures | début de la dernière coupure d'Internet, et sa durée en minutes, en cours ou finie |
| Coupures sur 24 h, Coupures sur 30 jours | coupures | nombre de coupures ; celles du réseau local ne comptent pas |
| Dernier redémarrage du modem | coupures | heure du dernier redémarrage constaté |
| Erreurs non corrigées sur 24 h | mesure | données perdues sur la ligne |
| Canaux à surveiller, Puissance descendante minimale et maximale, SNR le plus bas, Émission la plus forte | mesures | les valeurs de la vue d'ensemble |
| Latence, Pertes | mesures | dernière tranche de la sonde |
| Canaux descendants, Canaux montants, Dernier relevé | diagnostic | état de la collecte |

Quand Mire sait redémarrer le modem, l'appareil porte aussi un bouton **Redémarrer le modem** (`button.mire_redemarrer`), utile dans une automatisation, après une coupure prolongée par exemple. Il passe par le même chemin que la page Modem : une seule demande par tranche de 10 minutes, inscrite au journal. Un message retenu sur le sujet de commande n'est jamais pris pour un appui. Il se retire dans les réglages (`MIRE_MQTT_REDEMARRAGE=false`).

Les identifiants suivent le nom de la donnée : `binary_sensor.mire_internet`, `binary_sensor.mire_signal`, `binary_sensor.mire_modem`, `sensor.mire_diagnostic`, `sensor.mire_derniere_coupure`, etc. Exemple d'automatisation, prévenir quand le signal se dégrade :

```yaml
automation:
  - alias: Mire, signal dégradé
    triggers:
      - trigger: state
        entity_id: binary_sensor.mire_signal
        to: "on"
        for: "00:15:00"
    actions:
      - action: notify.notify
        data:
          title: Ligne câble
          message: "{{ states('sensor.mire_diagnostic') }}"
```

L'état est publié à chaque changement et retenu par le serveur ; si Mire s'arrête, ses capteurs passent indisponibles. Les sujets sont `mire/etat`, `mire/disponibilite` et `mire/commande/redemarrer` : un seul Mire par serveur MQTT.

## Supervision

`GET /sante` répond sans authentification, et sans rien révéler de la ligne :

```json
{"statut": "ok", "collecte": "a_jour", "dernier_releve": "2026-10-25T19:50:05Z", "modem": "Technicolor CGA4233 (firmware VOO)", "version": "v0.1.0"}
```

`collecte` vaut `a_jour`, `en_retard` (aucun relevé depuis trois intervalles, au moins 30 minutes), `en_attente` ou `inactive`. Une sonde Uptime Kuma de type « HTTP mot-clé » sur `a_jour` alerte dès que le modem ne répond plus.
