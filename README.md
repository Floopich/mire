<p align="center">
  <img src="docs/mire-logo.svg" alt="Mire" width="112">
</p>

<h1 align="center">Mire</h1>

<p align="center">
  <strong>Votre opérateur dit que tout va bien. Mire montre la chronologie.</strong>
</p>

<p align="center">
  Fork de <a href="https://github.com/itsDNNS/docsight">DOCSight</a> réduit à un seul matériel :
  le CGA4233, plus le mode routeur générique.
</p>

---

## Différences avec l'amont

| Domaine | DOCSight | Mire |
|---|---|---|
| Drivers | 21 familles de modems | 1 modem family (`voo_cga4233`) + `Generic Router` |
| Modem par défaut | `fritzbox` | `generic` |
| Seuils intégrés | `docsight.thresholds_vfkd` | `mire.thresholds_voo` |
| Module BNetzA | présent | supprimé |
| Langues | 24 | fr, nl, de, en |
| Langue par défaut | `en` | `fr` |
| Opérateur par défaut | vide | `VOO` |
| Seuils de repli de l'analyseur | valeurs VFKD | valeurs VOO |
| Marque affichée | DOCSight | Mire |
| Courrier de plainte | BNetzA / ARCEP | Service de médiation pour les télécommunications |

## État de ce dépôt

Le delta est **déjà appliqué** : 6 fichiers modifiés (`app/drivers/__init__.py`, `app/config.py`,
`app/main.py`, `app/collectors/__init__.py`, `app/blueprints/__init__.py`, `app/web.py`) et
24 fichiers supprimés.

Le driver `voo_cga4233` et le profil de seuils VOO sont intégrés. Rien à déposer.

Le driver est autonome : il hérite directement de `ModemDriver`, sans dépendance à un autre
driver. La séquence d'authentification double PBKDF2-SHA256, la gestion de session et la
politique de reprise sont intégrées dans `app/drivers/voo_cga4233.py`.

`app/drivers/` ne contient donc plus que `voo_cga4233.py`, `generic.py` et l'infrastructure
partagée (`base.py`, `registry.py`, `utils.py`, `formats/`, `format_compat.py`).

## Campagne de mesure

Un dossier de données par site, pour ne pas mélanger deux lignes dans la même base.

```bash
./scripts/site.sh start dupont     # cree sites/dupont/data, ecrit .env, demarre
./scripts/site.sh archive dupont   # arrete et produit sites/dupont-AAAAMMJJ.tar.gz
./scripts/site.sh list
```

L'image vient de GHCR. Le compte propriétaire est déduit du remote git et écrit dans `.env` au
premier `start` — rien à éditer. Pour construire localement à la place, décommenter `build: .` — à éviter sur un Pi 3, la compilation des helpers
C et l'installation pip avec vérification de hachages y sont pénibles avec 1 Go de RAM.

### Boîtier itinérant

Trois points comptent quand le collecteur passe de ligne en ligne :

- **Une horloge sauvegardée.** Un Pi sans RTC prend l'heure par NTP au démarrage. Si la ligne
  tombe — l'événement même qu'on veut prouver — un redémarrage sans réseau repart sur une heure
  fausse et les horodatages deviennent inopposables. Un DS3231 en I²C règle le problème :
  `dtoverlay=i2c-rtc,ds3231` dans `/boot/firmware/config.txt`, puis purger `fake-hwclock`.
- **Le modem n'est pas toujours en bridge.** L'adresse `192.168.100.1` et les identifiants sont
  à saisir sur place, pas à supposer.
- **L'accès distant vaut accès à un réseau tiers.** Le prévenir, et délier l'appareil du compte
  à la fin de chaque campagne.

### Durée

Une à deux semaines par site. Une session courte ne capte pas les dégradations d'heure de pointe,
qui sont l'essentiel de ce qu'on cherche à documenter.

`scripts/mire-fork.sh` reste dans le dépôt pour réappliquer le delta après un merge de l'amont :
les fichiers de drivers reviennent avec le merge, le script les re-supprime.

## Le script

`scripts/mire-fork.sh` supprime tous les drivers sauf `generic.py` et `voo_cga4233.py`,
réécrit `app/drivers/__init__.py` avec les deux seules entrées du registre, bascule les
valeurs par défaut de `modem_type` de `fritzbox` vers `generic`, et retire la fonction
d'utilisation de segment.

Il s'arrête si `app/drivers/voo_cga4233.py` est absent, si un motif de patch ne correspond plus
(l'amont a bougé), s'il reste une référence orpheline, ou si un fichier Python ne parse plus —
les 164 fichiers de `app/` sont vérifiés à la fin.

À relancer après chaque merge de l'amont :

```bash
git fetch upstream && git merge upstream/main
./scripts/mire-fork.sh
```

## Seuils

`mire.thresholds_voo` remplace le profil allemand dans `app/threshold_profiles.py`.

Les lignes `ofdm` de `downstream_power` et `snr` ne viennent pas de la pratique VOO mais de la
spec CableLabs DOCSIS 3.1 PHY, reprises du profil VFKD. Sans elles, un canal OFDM serait jugé
sur le `good_min` de 40 dB du 4096QAM alors que son MER agrégé se mesure autrement, et sortirait
en critique alors qu'il va bien.

## Débits souscrits

Le modem n'expose aucune information WAN en mode bridge, donc le driver renvoie un dict vide.
Le rapport se replie sur les réglages existants `booked_download` / `booked_upload`
(Paramètres > Speedtest, en Mbit/s), qui servaient déjà au calcul de santé du speedtest.

```bash
BOOKED_DOWNLOAD=1000 BOOKED_UPLOAD=50 ./scripts/site.sh start dupont
```

Non renseignés, la ligne tarifaire affiche « N/A » — préférable à la valeur d'un autre
abonnement dans un document destiné à appuyer une plainte.

## Courrier de plainte

Les textes visaient la BNetzA en anglais et l'ARCEP en français. Ils sont réécrits sur la
procédure belge : plainte écrite au service de traitement des plaintes de l'opérateur, puis, à
défaut de solution dans un délai raisonnable, saisine du Service de médiation pour les
télécommunications — instance de recours gratuite instituée auprès de l'IBPT par la loi du
21 mars 1991, entité qualifiée au sens du livre XVI du Code de droit économique. L'IBPT ne
traite pas les litiges individuels.

Adapté dans les quatre langues conservées. Le néerlandais et l'allemand comptent : ce sont des
langues officielles belges, et la Communauté germanophone est en Wallonie.

Les 20 autres locales sont supprimées de `app/i18n`, `app/modules/reports/i18n` et
`app/modules/modulation/i18n`. Le sélecteur de langue est construit à partir des fichiers
présents, il n'affiche donc plus que les quatre. Une locale inconnue retombe sur l'anglais.

## Cohérence

- Le repli codé en dur de `app/analyzer.py`, utilisé quand aucun profil de seuils n'est chargé,
  reprenait les valeurs allemandes. Il porte désormais les valeurs VOO : une instance fraîche
  analyse correctement avant même l'activation du profil.
- `isp_name` vaut `VOO` par défaut, sinon le courrier s'ouvre sur « Service technique de , ».
- L'onglet « utilisation de segment » est retiré de la barre latérale avec son gabarit, la
  fonction ayant été supprimée.
- La marque affichée passe à Mire dans les gabarits et les chaînes traduites. Les identifiants
  techniques ne bougent pas : préfixes de clés `mire.`, nom de la base, identifiants de
  modules, `packaging/windows/mire.spec` et son icône.
- `.github/workflows/image.yml` est supprimé : il faisait doublon avec `docker.yml`, qui publie
  la même image sur les mêmes tags et couvre en plus `linux/arm/v7`.

## Tests

Les tests des drivers et modules supprimés sont retirés par `scripts/mire-fork.sh`.
Le workflow `windows-desktop.yml` est conservé : il produit une application desktop Windows
autonome depuis `packaging/windows/`, utile pour installer Mire chez un abonné qui ne veut pas
de Docker. Le spec PyInstaller découvre les modules de `app/` dynamiquement, il ne référence
aucun driver par son nom — la réduction des drivers ne le casse pas.

Il reste des fichiers de tests qui **mentionnent** du code supprimé sans lui être dédiés —
`conftest.py`, les tests de collecteurs et de vues web utilisent des noms de drivers comme
valeurs de fixture. Ils demandent des retouches, pas des suppressions. À traiter en lançant la
suite localement, ce que je n'ai pas pu faire ici.

## Retiré

Le module BNetzA (`app/modules/bnetz`) est supprimé : c'est un importeur du fichier CSV produit
par l'outil de mesure officiel allemand, sans équivalent publié par l'IBPT. Toutes ses
références externes sont sous `try/except` ou en paramètre optionnel, la suppression dégrade
proprement — les sections correspondantes du rapport et du journal restent simplement vides.

La mesure d'utilisation de segment est câblée au FRITZ!Box et n'a pas d'équivalent ici :
`app/fritzbox.py`, `app/collectors/segment_utilization.py` et `app/blueprints/segment_bp.py`
sont supprimés, le blueprint désenregistré, le collecteur retiré de la boucle, la carte
correspondante retirée des modules et le réglage passé à `False` par défaut.

## Effets de bord connus

- `app/main.py` importe `TransientHtmlChannelPageError` depuis le driver Surfboard dans un
  `try/except ImportError` avec une classe de repli : sa suppression est sans effet.
- Les fichiers `app/i18n/*.json` gardent les libellés des modems supprimés. Sans conséquence
  fonctionnelle.

## Licence et marque

Fork MIT de DOCSight, Copyright (c) 2026 Dennis Braun — voir [LICENSE](LICENSE).

Mire est **basé sur DOCSight** sans en être une version officielle. Le nom et le logo DOCSight
relèvent de la politique de marque du projet amont
([TRADEMARKS.md](https://github.com/itsDNNS/docsight/blob/main/TRADEMARKS.md)) et ne sont pas
repris ici.
