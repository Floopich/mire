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

## Deux noms pour la meme couleur d accent

**Statut : cosmetique, non corrige.**

Le CSS utilise deux familles de variables pour la meme chose :
`--accent` / `--accent-hover` et `--amethyst` / `--amethyst-light`. Les
quatorze themes definissent les deux, donc l affichage est correct quel
que soit le theme. Mais le nom `amethyst` vient de la palette violette de
DOCSight et n a plus de sens dans un projet corail.

Repartition actuelle : environ deux tiers des usages passent encore par
`var(--amethyst*)`, un tiers par `var(--accent*)`, souvent dans le meme
bloc de regles.

A faire : remplacer `var(--amethyst)` par `var(--accent)` et
`var(--amethyst-light)` par `var(--accent-hover)` partout, puis retirer
les trois definitions de `tokens.css` et les surcharges correspondantes
des quatorze themes. Aucun test ne couvre les couleurs : verification
visuelle necessaire sur les quatorze themes, en clair et en sombre.
