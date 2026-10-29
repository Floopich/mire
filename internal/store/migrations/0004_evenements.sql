-- Journal des evenements de la ligne : changement d'etat, redemarrage du
-- modem, canal perdu, modulation, pic d'erreurs, modem injoignable.
--
-- Les evenements sont deduits des releves au moment ou ils arrivent, puis
-- figes : un changement de seuils ne reecrit pas l'histoire, il s'applique
-- aux releves suivants.
CREATE TABLE events (
    id         INTEGER PRIMARY KEY,
    ts         INTEGER NOT NULL,              -- epoch en secondes, UTC
    kind       TEXT    NOT NULL,
    level      INTEGER NOT NULL DEFAULT 0,    -- 0 inconnu, 1 bon, 2 tolere, 3 a surveiller, 4 critique
    direction  TEXT    NOT NULL DEFAULT '',   -- 'descendant', 'montant' ou vide
    channel_id INTEGER NOT NULL DEFAULT 0,    -- 0 : plusieurs canaux ou aucun
    message    TEXT    NOT NULL
);
CREATE INDEX idx_events_ts ON events(ts);
CREATE INDEX idx_events_kind_ts ON events(kind, ts);
