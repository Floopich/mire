# Charte graphique de Mire

Mire suit **Material 3 Expressive**.

## Logo

Cinq barres de niveau en V ; celle du centre, plus courte et corail, passe sous un seuil corail en pointillés.

- Fichier de référence : `internal/webview/assets/mire.svg` (128 × 128), utilisé aussi comme icône d'onglet.
- Couleurs fixes, identiques en clair et en sombre : gris `#808080`, corail `#E8817F`. Le corail est aussi la couleur source du schéma Material.
- Tailles : 16 px (onglet), 36 px (barre d'application), 88 px (connexion).
- Icône d'application : le logo sur une tuile claire arrondie (`#FFF8F7`, rayon 22 %), 192 et 512 px ; variante *maskable*, aussi icône iOS (180 px). Sources dans `tools/icones/`.
- Couleur de la fenêtre : corail `#E8817F`, en clair comme en sombre.

## Couleurs

Schéma généré par `material-color-utilities` (spécification 2025, variante *Tonal Spot*) depuis la couleur source **#E8817F**. Rôles Material (`--md-sys-color-primary`, `surface-container`…), clair et sombre déclarés avec `light-dark()`.

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

Couleurs personnalisées Material (couleur, encre, conteneur, encre du conteneur), non harmonisées avec la source. Les messages d'information restent neutres.

| Niveau | Couleur claire / sombre | Conteneur clair / sombre | Forme |
|---|---|---|---|
| Bon | `#1B6D24` / `#88D982` | `#A3F69C` / `#005312` | biscuit, festons doux |
| Toléré | `#005EB5` / `#A8C8FF` | `#D6E3FF` / `#00468A` | fleur à six pétales |
| À surveiller | `#7B5800` / `#F9BD3C` | `#FFDEA6` / `#5D4200` | soleil à huit pointes |
| Critique | `#B91D20` / `#FFB4AC` | `#FFDAD6` / `#93000E` | éclat à douze pointes |
| Non qualifié | outline | surface-container-highest | cercle |

Régénérer : `cd tools/couleurs && npm install && npm run schema`, puis reporter les valeurs dans `mire.css`.

## Formes

- Échelle de coins Material 3 Expressive : 4, 8, 12, 16, 20, 28, 32, 48 px et plein.
- Verdict : 48 px. Cartes : 28 px. Chiffres clés et groupes de listes : 20 px. Boutons et barres de la bande de canaux : pleins.
- Formes expressives calculées dans `shapes.go` : badge du verdict, puce de canal, légende.
- Morphose : bouton pressé, élément de liste, segment choisi.

## Typographie

- **Roboto Flex**, police variable réduite au latin (graisse, chasse, taille optique), 172 Ko.
- Échelle Material 3 : display (36 à 45 px, accentuée : graisse 640, chasse 112 %), headline small 24, title large 22, title medium 16, body 16 et 14, label 14 et 12.
- Chiffres tabulaires partout. Nombres à la française : virgule, vrai signe moins, espace insécable avant l'unité, espace fine pour les milliers.

## Pages

Sans script : la vue d'ensemble se recharge par `meta refresh`, les champs d'une option se dévoilent avec `:has()`. Un champ imposé par une variable est grisé et nomme sa variable.

## Vocabulaire

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

## Licences des ressources

- Roboto Flex : SIL Open Font License 1.1 (`internal/webview/assets/roboto-flex-OFL.txt`).
- Pictogrammes Material Symbols : Apache License 2.0 (`internal/webview/assets/material-symbols-LICENSE.txt`).
