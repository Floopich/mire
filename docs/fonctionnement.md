# Fonctionnement

Ce que Mire mesure, et comment il en juge. Dans l'interface, la page **Comprendre la ligne** explique chaque mesure avec les seuils de l'opérateur et vos propres valeurs.

![Comprendre la ligne](images/comprendre.png)

## État de la ligne

Mire relève les tables DOCSIS du modem toutes les 10 minutes, et toutes les minutes pendant le quart d'heure qui suit un incident. Chaque canal est jugé selon les seuils du profil d'opérateur : bon, toléré, à surveiller ou critique.

- Les **erreurs** se jugent sur les dernières 24 heures, et non sur les compteurs du modem, qui cumulent depuis son démarrage : un canal n'est signalé que s'il perd vraiment des données, au moins 1 000 erreurs non corrigées et une part notable de ses erreurs.
- Une **mesure douteuse** est reconnue : un canal OFDM dont la puissance s'écarte de plus de 10 dB de ses voisins SC-QAM, avec une réception excellente, est signalé comme une mesure probablement erronée du modem.
- **Modulation habituelle OFDMA** : certains segments fonctionnent durablement en basse modulation. Déclarée dans les réglages, elle devient la référence : un cran en dessous est à surveiller, deux crans critique.

## Journal des événements

Chaque relevé est comparé au précédent, et ce qui a changé est inscrit avec son heure. Le journal répond à la question que pose une ligne capricieuse : que s'est-il passé, et quand ? Une dérive qui revient chaque soir, un redémarrage du modem au milieu de la nuit, une rafale d'erreurs pendant un match s'y lisent d'un coup d'œil.

| Événement | Ce qui le déclenche |
|---|---|
| Changement d'état | la ligne passe de bon à à surveiller ou critique, ou y revient ; la cause est nommée |
| Redémarrage du modem | les compteurs d'erreurs repartent de zéro sur presque tous les canaux |
| Canaux perdus, retrouvés | un canal disparaît du relevé, ou y revient |
| Modulation | un canal change d'ordre de modulation, en baisse ou en hausse |
| Pic d'erreurs | plus de 1 000 erreurs non corrigées entre deux relevés ; la fin du pic en donne le total |
| Saut de puissance | la puissance moyenne bouge de plus de 2 dB en voie descendante, 3 dB en voie montante, entre deux relevés, même dans la plage nominale : souvent un connecteur ou un amplificateur |
| Modem injoignable | deux relevés manqués de suite ; son retour donne la durée sans relevé |

**Notes** : le journal accepte aussi vos repères, datés à la minute : « technicien passé », « connecteur remplacé », « modem changé ». Ils situent un changement de la ligne par rapport à une intervention, et se suppriment d'un clic. Une note n'est jamais envoyée en notification.

Les derniers événements s'affichent sur la vue d'ensemble, ceux d'un canal sur sa page, à côté de ses erreurs relevé par relevé. Le journal suit la conservation des relevés.

## Internet

Le modem dit l'état du signal, pas si Internet répond. La sonde le vérifie toutes les 30 secondes en ouvrant une connexion TCP vers trois résolveurs publics (Cloudflare `1.1.1.1`, Google `8.8.8.8`, Quad9 `9.9.9.9`, port 443). Elle ne transmet rien : la connexion est refermée dès qu'elle est établie. TCP plutôt que ping, parce que le ping exige des privilèges que le conteneur n'a pas.

- **Coupure** : trois tentatives sans réponse de suite, soit environ 90 secondes. Elle est datée de la première tentative manquée.
- **Cause** : pendant une coupure, la sonde interroge le modem. S'il répond, c'est la ligne ou l'opérateur ; sinon, le réseau local ou la machine de Mire.
- **Latence et pertes** : agrégées par tranches de cinq minutes, avec la disponibilité sur la période.
- **Heure par heure** : trente jours de mesures repliés sur une journée, latence et pertes de chaque heure. Une ligne saturée le soir, quand le quartier regarde des vidéos, s'y voit d'un coup d'œil ; Mire le dit en une phrase et colore les heures chargées. Le profil apparaît après trois jours de mesures.
- **Compensation légale (Belgique)** : pour une coupure de la ligne de plus de 8 heures, Mire indique discrètement la compensation prévue par la loi (article 113/2 de la loi du 13 juin 2005) : 1 € à partir de 8 heures, 2 € à partir de 24 heures, puis 0,50 € par jour, ou un trentième de l'abonnement par jour entamé si c'est plus.

![Profil heure par heure](images/profil-horaire.png)

- **Débit** : lu dans Speedtest Tracker ou sur une passerelle UniFi, voir [Intégrations](integrations.md#débit).

La page **Internet** rassemble ces mesures, et le journal inscrit le début et la fin de chaque coupure. La sonde fonctionne sans modem configuré ; elle se désactive, ou change de cibles, dans les réglages.

## Modem

La page **Modem** dit ce que le modem annonce de lui-même : modèle, version du logiciel, état de la ligne câble, durée depuis le dernier démarrage, et mode.

- **En mode pont** (bridge), le modem passe la connexion à votre routeur : le Wi-Fi et les appareils se gèrent sur celui-ci, et la page s'en tient au modem.
- **En mode routeur**, elle montre aussi le réseau local (adresse, serveur DHCP, DNS), chaque réseau Wi-Fi (nom, canal, largeur, norme, sécurité) et les appareils connus du modem, connectés d'abord. Tout est en lecture seule : ces réglages se modifient dans l'interface du modem, dont la page donne le lien.
- **Redémarrer le modem** (le bouton « Redémarrer le système » de l'interface du modem) passe par une page de confirmation, qui rappelle qu'Internet, le Wi-Fi du modem et le téléphone fixe seront coupés 3 à 5 minutes. La demande est inscrite au journal, et une seconde demande est refusée pendant 10 minutes. Mire dit si le modem a accepté, refusé ou n'a pas répondu, sans jamais rejouer une demande restée sans réponse.

Le modem n'accepte qu'une session à la fois : la page et la collecte partagent la même, et passent l'une après l'autre. Un modèle dont le pilote ne sait pas encore lire ces informations garde ses relevés ; la page le dit simplement.

![Page Modem, modem en mode routeur](images/modem.png)

## Tendance et météo

La page **Tendance**, ouverte depuis la vue d'ensemble, trace les moyennes journalières des canaux SC-QAM sur 30 jours, 90 jours ou un an, et dit si chaque mesure est stable, en hausse ou en baisse, en dB par mois. Les canaux OFDM et OFDMA sont écartés : leur arrivée, leur départ ou une mesure douteuse du modem déplaceraient la moyenne sans que la ligne ait changé.

Avec la météo activée, Mire relève toutes les six heures la température du lieu choisi auprès d'[Open-Meteo](https://open-meteo.com), service gratuit sans compte : seule la position lui est envoyée. Après 14 jours, la page dit si la ligne suit la température, et de combien : un câble extérieur ou un amplificateur sensible au climat se trahit ainsi. La météo s'active dans **Réglages, Météo** (ou `MIRE_METEO=true` et `MIRE_METEO_POSITION=Namur`).

## Rapport de la ligne

La page **Rapport**, ouverte depuis la vue d'ensemble, fait le bilan d'une période, et s'imprime telle quelle (Ctrl+P, puis « Enregistrer au format PDF ») :

- une synthèse en quelques phrases : temps passé en bon état, à surveiller ou critique, coupures d'Internet et la plus longue, redémarrages du modem, canaux sortis de la plage nominale, erreurs non corrigées ;
- chaque canal sur la période : puissance minimale, maximale et moyenne, SNR, erreurs non corrigées, part du temps hors plage ;
- le débit médian, avec Speedtest Tracker ou une passerelle UniFi ;
- les coupures, avec la compensation légale estimée chez un opérateur belge, et le journal des alertes et des notes.

Une coupure du réseau local est signalée à part : elle relève de l'installation, pas de l'opérateur. Avec une **date d'intervention**, le rapport compare les 7, 14 ou 30 jours qui la précèdent à ceux qui la suivent, mesure par mesure : de quoi vérifier qu'un passage de technicien a réglé le problème.

**Données de la période** : chaque tableau se télécharge en CSV, pour un tableur ou un outil d'analyse. Canaux descendants et montants relevé par relevé, sonde Internet par tranche de cinq minutes, coupures, débit et journal ; heure locale au format ISO 8601, virgule entre les colonnes, point décimal. Les erreurs des canaux sont celles survenues depuis le relevé précédent, et non les compteurs cumulés du modem.
