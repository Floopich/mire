-- Micro-coupures : au moins deux tentatives de suite de la sonde sans
-- reponse, sans atteindre la duree d'une coupure. La duree n'est connue
-- qu'a l'intervalle de la sonde pres : on garde ses deux bornes.
CREATE TABLE IF NOT EXISTS micro_cuts (
    ts    INTEGER PRIMARY KEY,   -- premiere tentative sans reponse, epoch UTC
    min_s INTEGER NOT NULL,      -- duree certaine, en secondes
    max_s INTEGER NOT NULL,      -- duree maximale, en secondes
    kind  TEXT    NOT NULL       -- ligne, local ou internet, comme une coupure
);

-- Debut de la detection : avant, l'absence de micro-coupure ne prouve rien.
CREATE TABLE IF NOT EXISTS micro_since (
    ts INTEGER NOT NULL
);
INSERT INTO micro_since(ts) SELECT CAST(strftime('%s', 'now') AS INTEGER)
WHERE NOT EXISTS (SELECT 1 FROM micro_since);
