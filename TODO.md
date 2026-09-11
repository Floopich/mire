# TODO

Points ouverts, laissés volontairement en l'état. Chacun indique ce qui est
vérifié, ce qui ne l'est pas, et ce qu'il faudrait pour trancher.

---

## 1. OFDMA en 16QAM -- resolu

L'OFDMA dispose de son propre bareme dans `upstream_modulation.ofdma`
(critical 32, warning 64, tolerated 128), distinct de celui du SC-QAM.

Pour une ligne dont la porteuse OFDMA montante est en basse modulation
depuis l'installation, le reglage `ofdma_low_qam_expected` (Parametres >
Connexion, ou la variable `OFDMA_LOW_QAM_EXPECTED`) abaisse les bornes a
8 : un canal en 16QAM ressort alors `tolerated` au lieu de `critical`, et
la ligne ne bascule plus en critique en permanence.

Reste utile : relever les modulations reelles sur plusieurs lignes VOO
pour verifier que les bornes par defaut de l'OFDMA sont les bonnes.

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

**Statut : captures supprimees du depot, a refaire.**

Les captures de `docs/screenshots/` et `app/static/screenshots/` dataient du
31 aout, avant le rebranding corail : elles montraient encore la marque et
l accent violet de DOCSight, et pour certaines des vues supprimees depuis
(BQM, segment). Elles ont ete retirees au passage du depot en public.

En attendant les nouvelles : `docs/index.html` affiche le logo a la place,
l apercu social (og:image et twitter:image) est desactive, et le manifeste
PWA n a plus de cle screenshots. Les trois sont a retablir une fois les
captures Mire disponibles.

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

