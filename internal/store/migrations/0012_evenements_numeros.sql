-- Numeros d'evenements jamais reutilises. Sans AUTOINCREMENT, SQLite
-- redonne le plus grand numero libere : une note supprimee juste apres sa
-- creation cedait son numero a l'evenement suivant, que les notifications,
-- deja passees par ce numero, n'envoyaient jamais.
CREATE TABLE events_numerotes (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    ts         INTEGER NOT NULL,
    kind       TEXT    NOT NULL,
    level      INTEGER NOT NULL DEFAULT 0,
    direction  TEXT    NOT NULL DEFAULT '',
    channel_id INTEGER NOT NULL DEFAULT 0,
    message    TEXT    NOT NULL
);
INSERT INTO events_numerotes(id, ts, kind, level, direction, channel_id, message)
    SELECT id, ts, kind, level, direction, channel_id, message FROM events;
DROP TABLE events;
ALTER TABLE events_numerotes RENAME TO events;
CREATE INDEX idx_events_ts ON events(ts);
CREATE INDEX idx_events_kind_ts ON events(kind, ts);
-- Le compteur repart au-dela du dernier evenement deja traite par les
-- notifications, pour le cas ou ce numero aurait ete libere avant cette
-- migration.
INSERT INTO sqlite_sequence(name, seq)
    SELECT 'events', 0 WHERE NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'events');
UPDATE sqlite_sequence SET seq = max(seq,
    coalesce((SELECT CAST(value AS INTEGER) FROM settings WHERE key = 'notif.dernier_evenement'), 0))
    WHERE name = 'events';
