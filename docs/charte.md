# Charte graphique de Mire

Mire suit **Material 3 Expressive**, le langage visuel de Google en vigueur depuis 2025 : couleurs dynamiques, formes expressives, typographie variable accentuée, mouvement à ressort. Le nom vient de la mire de réglage, l'image qui permettait de juger d'un coup d'œil la qualité d'un signal : l'interface doit dire en une seconde si la ligne va bien, puis permettre de comprendre pourquoi.

## Principes

1. **L'état d'abord.** Le verdict ouvre la page dans un grand conteneur coloré par son niveau.
2. **Couleur et forme portent l'information.** Chaque niveau a sa couleur et sa silhouette : un état reste lisible sans distinguer les couleurs.
3. **Une absence n'est pas un zéro.** Une mesure manquante s'affiche « — » ; une courbe s'interrompt au lieu de relier deux points.
4. **Rien d'extérieur.** Police embarquée, aucun script, aucune ressource tierce. La politique de sécurité n'autorise que la feuille de style, la police et les images de l'instance.
5. **Le français courant.** « Relevé », « voie montante », « à surveiller » ; jamais « courriel » ni « authentifiant ».

## Logo

Cinq barres de niveau, disposées en V ; celle du centre, plus courte et corail, passe sous un seuil corail en pointillés. C'est la lecture de Mire en une image : des canaux, un seuil, et celui qui décroche.

- Fichier de référence : `internal/webview/assets/mire.svg` (128 × 128), utilisé aussi comme icône d'onglet.
- Couleurs fixes, identiques en clair et en sombre : gris `#808080`, corail `#E8817F`. Le corail est aussi la couleur source du schéma Material.
- Tailles : 16 px (onglet), 36 px (barre d'application), 88 px (connexion).
- Icône d'application : le logo sur une tuile claire arrondie (`#FFF8F7`, rayon 22 %), en 192 et 512 px. Variante *maskable* à fond plein, logo dans la zone sûre centrale, que le système découpe à sa forme ; elle sert aussi d'icône iOS (180 px). Sources et génération dans `tools/icones/`.
- Couleur de la fenêtre : corail `#E8817F`, en clair comme en sombre, pour la barre d'adresse du navigateur, la barre d'état et la barre de navigation de l'application installée, et l'écran de lancement. Le système y pose des icônes sombres, lisibles sur les deux thèmes.

## Couleurs

Le schéma est **généré** par `material-color-utilities` (bibliothèque officielle Material), spécification 2025, variante *Tonal Spot*, à partir de la couleur source **#E8817F** (corail). Les rôles suivent la nomenclature Material : `--md-sys-color-primary`, `on-primary`, `primary-container`, `surface-container-low` à `highest`, etc. Clair et sombre sont déclarés ensemble avec `light-dark()`.

| Rôle | Clair | Sombre | Usage |
|---|---|---|---|
| primary | `#8C4D4C` | `#F8B6B3` | courbes, liens, bouton principal |
| secondary | `#785756` | `#E6BDBB` | période sélectionnée |
| secondary-container | `#FFDAD8` | `#503433` | boutons tonals |
| surface | `#FFF8F7` | `#130C0C` | fond de page |
| surface-container | `#FEE9E8` | `#211716` | éléments de liste, chiffres clés |
| surface-container-highest | `#F5DDDB` | `#302222` | messages d'information |
| on-surface-variant | `#6D5B5A` | `#BCA6A5` | textes secondaires |

### États

Les couleurs d'état sont des **couleurs personnalisées** Material, déclinées en quatre rôles : couleur, encre, conteneur, encre du conteneur. Elles ne sont **pas harmonisées** avec la source : une source chaude rapprocherait l'avertissement du critique, et un état doit rester reconnaissable quelle que soit la couleur de la marque. Les messages d'information restent neutres pour ne jamais ressembler à une alerte.

| Niveau | Couleur claire / sombre | Conteneur clair / sombre | Forme |
|---|---|---|---|
| Bon | `#1B6D24` / `#88D982` | `#A3F69C` / `#005312` | biscuit, festons doux |
| Toléré | `#005EB5` / `#A8C8FF` | `#D6E3FF` / `#00468A` | fleur à six pétales |
| À surveiller | `#7B5800` / `#F9BD3C` | `#FFDEA6` / `#5D4200` | soleil à huit pointes |
| Critique | `#B91D20` / `#FFB4AC` | `#FFDAD6` / `#93000E` | éclat à douze pointes |
| Non qualifié | outline | surface-container-highest | cercle |

Pour régénérer le schéma après un changement de couleur source : `cd tools/couleurs && npm install && npm run schema`, puis reporter les valeurs dans le bloc de jetons de `mire.css`.

## Formes

- Échelle de coins Material 3 Expressive : 4, 8, 12, 16, 20, 28, 32, 48 px et plein.
- Verdict : 48 px. Cartes : 28 px. Chiffres clés et groupes de listes : 20 px. Boutons et barres de la bande de canaux : pleins.
- **Formes expressives** calculées dans `shapes.go` (biscuit, fleur, soleil, éclat, cercle), sur le modèle de la bibliothèque de formes Material : badge du verdict, puce de chaque canal portant son numéro, légende.
- **Morphose** : un bouton s'aplatit sous le doigt, un élément de liste s'arrondit, le segment choisi d'un groupe devient une pilule.

## Typographie

- **Roboto Flex**, police variable (licence SIL OFL, fichier joint), réduite au latin avec les axes graisse, chasse et taille optique : 172 Ko, servie par l'instance.
- Échelle Material 3 : display (36 à 45 px, accentuée : graisse 640, chasse 112 %), headline small 24, title large 22, title medium 16, body 16 et 14, label 14 et 12.
- Chiffres tabulaires partout. Nombres à la française : virgule, vrai signe moins, espace insécable avant l'unité, espace fine pour les milliers.

## Mouvement

Ressorts Material 3 Expressive convertis en courbes CSS `linear()` : *fast spatial* (raideur 800, amortissement 0,6) pour les morphoses, *slow spatial* (200 ; 0,8) pour l'entrée du badge du verdict, *fast effects* pour les couches d'état. Un seul mouvement non déclenché par l'utilisateur : l'entrée du badge. Tout est coupé si le système demande moins d'animations.

## Composants

| Composant Material 3 Expressive | Dans Mire |
|---|---|
| Barre d'application | logo, nom, accès à Internet, aux événements et aux réglages, bouton tonal « Se déconnecter » ; icônes seules sur téléphone |
| Conteneur coloré, forme expressive | verdict |
| Indicateur de progression ondulé | attente du prochain relevé |
| Groupe de boutons connectés | choix de la période |
| Liste groupée (éléments séparés, coins du groupe arrondis) | canaux, alignés en colonnes sur grand écran |
| Cartes | bande de canaux, chiffres clés, graphiques |
| Champ de saisie avec libellé flottant, bouton plein large | connexion, assistant |
| Liste d'événements : forme du niveau, heure, phrase | journal, derniers événements, événements d'un canal |
| Barres empilées | erreurs d'un canal : corrigées en gris (`outline`), non corrigées en rouge critique ; tentatives de la sonde sans réponse, en rouge critique |
| Courbe partant de zéro | latence de la sonde |
| Liste à deux lignes (titre, résumé), volet liste-détail | rubriques des réglages : côte à côte sur grand écran, l'une puis l'autre sur téléphone |
| Formulaire sur une colonne | une rubrique des réglages |
| Interrupteur principal (barre teintée) | active l'option d'une rubrique et dévoile ses champs |
| Interrupteur (piste et pouce qui grandit) | réglage oui ou non, à droite de son libellé |
| Menu déroulant à contour | choix du modèle, du profil, puis du service d'une option |
| Indicateur d'étapes | assistant de premier démarrage |
| Boutons radio en cartes | adresse du modem détectée |

La vue d'ensemble se recharge seule à l'arrivée du relevé suivant (`meta refresh`, sans script).

Sans script non plus, les options se dévoilent en CSS : `:has()` masque les champs d'une option désactivée, et n'affiche que ceux du service choisi dans une liste. Un navigateur sans `:has()` montre simplement tous les champs. Un champ imposé par une variable d'environnement est grisé, bordé de tirets, et nomme sa variable sous un cadenas.

## Rédaction

- Phrases courtes, voix active, sans jargon inutile.
- Un problème dit ce qui se passe et quoi faire : « La collecte est arrêtée : renseignez le mot de passe du modem dans les réglages ».
- Une page vide invite à agir : « Élargissez la fenêtre, ou vérifiez que la collecte est active. »
- Une action garde le même nom partout : « Se connecter », « Se déconnecter », « Revenir à la vue d'ensemble », « Enregistrer », « Enregistrer et tester ».
- Un échec technique se traduit en conseil ; le détail brut va au journal, pas à l'écran.
- Un événement tient en une phrase qui nomme la cause : « Ligne à surveiller : canal descendant 22, puissance descendante 8,7 dBmV, hors de la plage nominale. » Plusieurs canaux s'énumèrent à la française : « 3, 7 et 12 ».

### Vocabulaire

| Terme | Sens |
|---|---|
| relevé | une lecture complète des tables du modem, horodatée |
| événement | un changement constaté entre deux relevés, inscrit au journal |
| pic d'erreurs | une rafale d'erreurs non corrigées, du premier relevé touché au premier relevé calme |
| coupure | au moins trois tentatives de la sonde sans réponse de suite ; « ligne coupée » si le modem répond encore, « réseau local coupé » sinon |
| sonde | la vérification régulière qu'Internet répond, indépendante du modem |
| voie descendante, voie montante | du réseau vers le modem, du modem vers le réseau |
| canal | une porteuse DOCSIS, identifiée par son numéro et sa fréquence |
| plage nominale | l'intervalle de valeurs que l'opérateur juge normal |
| seuil | la limite basse d'une mesure, comme le SNR |
| non qualifié | mesure absente ou profil sans seuils : Mire ne juge pas |

Jamais « courriel », « authentifiant », ni anglicisme évitable.

## Licences des ressources

- Roboto Flex : SIL Open Font License 1.1 (`internal/webview/assets/roboto-flex-OFL.txt`).
- Pictogrammes Material Symbols : Apache License 2.0 (`internal/webview/assets/material-symbols-LICENSE.txt`).
