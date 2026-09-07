# TODO

Points ouverts, laissés volontairement en l'état. Chacun indique ce qui est
vérifié, ce qui ne l'est pas, et ce qu'il faudrait pour trancher.

---

## 1. OFDMA en 16QAM classé critique

**Statut : connu, laissé en erreur.**

Sur la capture réelle du 2026-08-30 (CGA4233, VOO, bridge), le canal OFDMA
montant utilise une modulation 16QAM. Le profil `mire.thresholds_voo` fixe
`upstream_modulation.warning_max_qam: 16` et `critical_max_qam: 4`. Le canal
ressort donc `critical`, et fait basculer toute la ligne en critique alors que
les 21 autres canaux sont bons.

Ces bornes ont vraisemblablement été écrites pour du SC-QAM, où descendre à
16QAM signale une voie retour dégradée. Pour un canal OFDMA, 16QAM est la
modulation par sous-porteuse et non un repli — mais **cette affirmation n'est
pas sourcée**.

Conséquence si on n'y touche pas : chez un abonné dont le modem utilise OFDMA
en 16QAM, Mire annonce une ligne critique en permanence. Le courrier de plainte
perd en crédibilité et les notifications de campagne restent en alerte.

À faire : relever les niveaux réels sur plusieurs lignes VOO, puis décider
d'exclure l'OFDMA de la règle `upstream_modulation` ou de lui donner ses
propres bornes.

---

## 2. Texte du courrier de plainte

Non relu. La procédure belge — opérateur puis Service de médiation pour les
télécommunications — a été rédigée en amont de ce chantier et n'a pas été
vérifiée depuis. Aucune formulation réglementaire ne doit être écrite sans
source.

---

## 3. Page de présentation publique

`docs/index.html` : les cinq promesses de fonctions supprimées ont été
retirées (mode démo, Windows Preview, compte de modems). Restent des éléments
hérités qui ne sont pas faux mais ne correspondent plus au public visé :

- les captures d'écran et le « proof pack » proviennent de données de
  démonstration générées par un mode qui n'existe plus ;
- `docs/samples/demo-complaint-report.pdf` est un exemple de plainte allemande.

---

## 4. Module UDM WAN Monitor

`community.udm_wan_monitor` est masqué du catalogue, activable via
`show_reserved_modules`. Il annonce viser les UDM Pro/SE ; la compatibilité
avec un UCG Ultra n'a pas été vérifiée. Son code est dans
`itsDNNS/docsight-modules`, sous licence à vérifier avant toute redistribution.

---

## 5. Contrôle de mise à jour

`app/runtime.py` interroge `api.github.com/repos/floopich/mire/releases/latest`.
Désactivé par défaut (`update_check_enabled: False`). Fonctionnera une fois le
dépôt publié **et** des releases taguées créées ; renverra 404 en silence
sinon.

---

## 6. Registres de contenu distants

Le catalogue de thèmes distant a été retiré (l'URL amont répondait 404 ; les
14 thèmes intégrés suffisent). Le catalogue de modules pointe toujours vers
`itsDNNS/docsight-modules`. À remplacer si tu publies `floopich/mire-modules`,
en y déposant au minimum le profil de seuils VOO.

---

## 7. Fixture de capture

`tests/drivers/fixtures/cga4233_voo_bridge.json` est une capture verbatim d'une
seule ligne, à un instant donné, sur une ligne en bon état. Elle ne couvre donc
aucun cas dégradé. Une capture prise pendant un incident chez un abonné aurait
une vraie valeur de non-régression.

---

## Pilote non verifie hors firmware VOO

**Statut : connu, assume.**

Le pilote `voo_cga4233` n'a ete valide que contre un CGA4233 sur firmware
VOO en bridge (2026-08-30). L'assistant propose desormais hey! et Orange,
qui distribuent le meme materiel sur le reseau VOO, avec l'identifiant
`admin` au lieu de `voo`. Cet identifiant est annonce, pas confirme par
un login reel, et rien ne garantit que les endpoints `/api/v1/modem/*`
ni la sequence PBKDF2 soient identiques sur ces firmwares.

A faire : au premier site hey! ou Orange visite par le collecteur,
confirmer l'identifiant et capturer une fixture, comme cela a ete fait
pour VOO dans `tests/drivers/fixtures/cga4233_voo_bridge.json`.

---

## Captures d ecran a refaire

**Statut : reporte, en attente de stabilisation de l interface.**

Les captures de `docs/screenshots/` et `app/static/screenshots/` datent du
31 aout, avant le rebranding corail : elles montrent encore l accent violet
de DOCSight, et pour certaines des vues supprimees depuis (BQM, segment).
Elles pesent environ 4,4 Mo au total et sont referencees par le README et
la page de presentation `docs/index.html`.

A refaire une fois l interface stabilisee : tableau de bord clair et sombre,
themes, parametres, speedtest, chronologie des canaux, correlation,
workflow de plainte. Verifier au passage qu aucune donnee de site reel
n apparait dessus.

## Logo Proximus a remplacer

**Statut : identifie, contournement applique.**

`app/static/img/providers/proximus.svg` pese 334 Ko contre 2 a 11 Ko pour
les autres logos. Le decapage des metadonnees n a gagne que 14 % : le
fichier est une vectorisation automatique d une image matricielle, avec
1030 chemins dont le premier est un simple rectangle.

A faire : le remplacer par un vrai logo vectoriel, ou par un PNG comme
`hey.png` (2 Ko) — `iconMap` accepte deja les deux formats.

