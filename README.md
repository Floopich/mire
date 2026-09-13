<p align="center">
  <img src="docs/mire-logo.svg" alt="Mire" width="112">
</p>

<h1 align="center">Mire</h1>

<p align="center">
  <strong>Analyser sa ligne DOCSIS, en continu et chez soi.</strong>
</p>

<p align="center">
  <sub>Basé sur <a href="https://github.com/itsDNNS/docsight">DOCSight</a> (licence MIT), adapté aux abonnés VOO en Belgique.</sub>
</p>

---

## Le problème

Votre modem câble sait exactement dans quel état est votre ligne. Il mesure en
permanence la puissance de chaque canal, le rapport signal/bruit, la modulation
négociée, les erreurs corrigées et non corrigées. Ces valeurs s'affichent dans son
interface, et les suivantes les remplacent quelques secondes plus tard.

Il n'en garde rien. Vous voyez donc un instantané, jamais une évolution — alors
qu'une ligne câble se dégrade presque toujours progressivement. Le bruit monte,
quelques canaux basculent en modulation plus basse, et vous ne constatez que les
symptômes : la visio qui se fige, la partie qui décroche, le débit qui s'effondre
en soirée.

## Ce que fait Mire

Mire interroge le modem à intervalle régulier et conserve chaque relevé dans une
base locale. Ce simple fait — garder l'historique — rend exploitables des chiffres
qui ne l'étaient pas.

**Collecte.** Puissances descendante et montante, MER/SNR par canal, modulation,
compteurs d'erreurs, pertes de synchronisation. Tout est horodaté et conservé.

**Analyse.** Chaque relevé est confronté à des seuils. Ceux de Mire viennent de la
pratique VOO plutôt que de valeurs génériques, ce qui évite de signaler comme
anormal un niveau parfaitement courant sur ce réseau.

**Détection.** Perte de synchronisation, chute de modulation, sortie de plage : ces
événements sont repérés et horodatés sans que vous ayez à regarder au bon moment.

**Restitution.** Graphiques par canal, tendances sur plusieurs semaines,
chronologies. C'est là qu'apparaissent les motifs répétitifs, comme la dégradation
quotidienne aux heures de pointe ou la dérive lente qui suit une intervention.

Ce cœur fonctionne seul, sans rien activer. Tout le reste est modulaire.

## Modules

Douze modules sont embarqués et s'activent individuellement dans les réglages.

| Module | Rôle |
|---|---|
| `comparison` | Comparer la qualité du signal entre deux périodes au choix |
| `modulation` | Distribution de modulation, exposition aux bas QAM |
| `connection_monitor` | Latence continue par sondes ICMP/TCP, perte de paquets, gigue |
| `speedtest` | Résultats Speedtest Tracker avec classification de santé |
| `weather` | Corréler la qualité du signal avec la température extérieure |
| `journal` | Documenter les incidents, y joindre des preuves, les regrouper |
| `evidence` | Guider d'une fenêtre d'incident vers un dossier exploitable |
| `reports` | Rapports d'incident PDF et courriers de plainte |
| `be_compensation` | Indemnité légale due pour une interruption (Belgique) |
| `be_mediation` | Parcours de plainte vers l'opérateur puis la médiation (Belgique) |
| `mqtt` | Publication vers Home Assistant avec auto-discovery |
| `backup` | Sauvegardes planifiées et restauration |

Les deux modules belges traitent le cas où le problème persiste malgré les échanges
avec l'opérateur : `be_compensation` calcule l'indemnité prévue par l'article 113/2
de la loi relative aux communications électroniques, `be_mediation` guide vers le
Service de médiation pour les télécommunications, instance de recours gratuite.
L'IBPT, lui, ne traite pas les litiges individuels.

## Matériel

**Modem** — le Technicolor CGA4233 en firmware VOO, joignable sur `192.168.100.1`.
Le mode bridge n'est pas nécessaire. L'adresse et les identifiants sont à vérifier
sur place plutôt qu'à supposer.

Pour tout autre modem, un **mode routeur générique** conserve les fonctions qui ne
dépendent pas du modem — latence, débits, journal, rapports — sans les données
DOCSIS.

**Collecteur** — un Raspberry Pi 3B+ suffit, sous Pi OS Lite 64 bits. N'importe
quelle machine faisant tourner Docker convient : NAS, Proxmox, Debian.

## Installation

```bash
docker run -d --name mire --restart unless-stopped \
  -p 1340:1340 -v mire_data:/data \
  ghcr.io/floopich/mire:latest
```

L'interface écoute sur le port **1340**. Ouvrez `http://<ip>:1340` et suivez
l'assistant, qui demande l'adresse du modem et ses identifiants.

[INSTALL.md](INSTALL.md) couvre le reste : installation sans Docker, reverse proxy,
sondes ICMP, mise à jour automatique, et le mot de passe administrateur généré au
premier démarrage.

## Réglages qui méritent une explication

### Seuils

Le profil `mire.thresholds_voo` porte les seuils qui qualifient l'état de la ligne.
Ils viennent de la pratique VOO, à deux exceptions près : les lignes `ofdm` de
`downstream_power` et `snr` suivent la spécification CableLabs DOCSIS 3.1 PHY.
Sans elles, un canal OFDM serait jugé sur le seuil de 40 dB du 4096QAM, alors que
son MER agrégé se mesure autrement — il ressortirait en critique tout en allant
parfaitement bien.

Le repli codé en dur dans `app/analyzer.py`, utilisé tant qu'aucun profil n'est
chargé, porte les mêmes valeurs. Une instance neuve analyse donc correctement dès
le premier relevé.

### Débits souscrits

En mode bridge, le modem n'expose aucune information sur l'abonnement. Renseignez
les débits dans Paramètres > Speedtest, en Mbit/s. Sans eux, les rapports affichent
« N/A » — préférable à une valeur empruntée à un autre abonnement dans un document
destiné à appuyer une réclamation.

## Aller plus loin

| | |
|---|---|
| [INSTALL.md](INSTALL.md) | Installation détaillée, reverse proxy, mise à jour |
| [ARCHITECTURE.md](ARCHITECTURE.md) | Fonctionnement interne, écriture d'un module |
| [DATA_CONTRACT.md](DATA_CONTRACT.md) | Format des données collectées |
| [SECURITY.md](SECURITY.md) | Exposition réseau, signalement de vulnérabilité |
| [SUPPORT.md](SUPPORT.md) | Où poser une question, comment signaler un problème |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Contribuer au projet |

Interface disponible en français, néerlandais, allemand et anglais. Image Docker
publiée sur GHCR pour amd64, arm64 et armv7.

## Licence et marque

Mire est un fork de [DOCSight](https://github.com/itsDNNS/docsight), sous licence
MIT. Le code d'origine est Copyright (c) 2026 Dennis Braun ; les ajouts propres à
Mire sont Copyright (c) 2026 Floopich. Voir [LICENSE](LICENSE).

Mire est **basé sur DOCSight** sans en être une version officielle. Le nom et le
logo DOCSight relèvent de la politique de marque du projet amont
([politique amont](https://github.com/itsDNNS/docsight/blob/main/TRADEMARKS.md),
résumée dans [TRADEMARKS.md](TRADEMARKS.md)) et ne sont pas repris ici.
