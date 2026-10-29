-- Mesures de debit, lues dans un Speedtest Tracker : une ligne par mesure
-- reussie. L'horodatage sert de cle : relire les memes mesures, ou changer
-- d'instance, ne cree pas de doublon.
CREATE TABLE IF NOT EXISTS speed_tests (
    ts            INTEGER PRIMARY KEY,   -- epoch en secondes, UTC
    download_mbps REAL    NOT NULL,
    upload_mbps   REAL    NOT NULL,
    ping_ms       REAL,
    jitter_ms     REAL,
    loss_pct      REAL,
    server        TEXT    NOT NULL DEFAULT ''
);
