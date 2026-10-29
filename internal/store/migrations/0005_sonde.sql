-- Mesures de la sonde de connexion, agregees par tranche de cinq minutes :
-- une mesure toutes les trente secondes pendant dix ans ferait dix millions
-- de lignes pour un graphique qui n'en affiche que quelques centaines.
CREATE TABLE probe_samples (
    ts      INTEGER PRIMARY KEY,           -- debut de la tranche, epoch UTC
    rounds  INTEGER NOT NULL,              -- tentatives dans la tranche
    failed  INTEGER NOT NULL,              -- tentatives sans aucune reponse
    rtt_min REAL,                          -- millisecondes, NULL sans reponse
    rtt_avg REAL,
    rtt_max REAL
);

-- link_events (migration 1) recoit les coupures de la sonde ; kind vaut
-- 'ligne' (Internet coupe, modem joignable), 'local' (ni l'un ni l'autre) ou
-- 'internet' (modem inconnu, cause indeterminee).
