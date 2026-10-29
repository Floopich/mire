-- Releve : une ligne par interrogation du modem.
CREATE TABLE readings (
    id       INTEGER PRIMARY KEY,
    ts       INTEGER NOT NULL,          -- epoch en secondes, UTC
    docsis   TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX idx_readings_ts ON readings(ts);

-- Canaux descendants.
--
-- ts est duplique depuis readings a dessein : l'index (channel_id, ts) devient
-- couvrant, et tracer une courbe sur plusieurs annees pour un canal se fait
-- sans jointure. Huit octets par ligne contre une jointure sur des dizaines de
-- millions de lignes, le compte est vite fait.
--
-- Les valeurs sont nullables : le modem omet certains champs selon le type de
-- canal, et un zero silencieux fausserait l'analyse plus surement qu'une
-- absence assumee.
CREATE TABLE ds_channels (
    reading_id      INTEGER NOT NULL REFERENCES readings(id) ON DELETE CASCADE,
    ts              INTEGER NOT NULL,
    channel_id      INTEGER NOT NULL,
    frequency_mhz   REAL,
    modulation      TEXT    NOT NULL DEFAULT '',
    power_dbmv      REAL,
    snr_db          REAL,
    corr_errors     INTEGER NOT NULL DEFAULT 0,
    noncorr_errors  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_ds_channel_ts ON ds_channels(channel_id, ts);
CREATE INDEX idx_ds_reading ON ds_channels(reading_id);

-- Canaux montants.
CREATE TABLE us_channels (
    reading_id      INTEGER NOT NULL REFERENCES readings(id) ON DELETE CASCADE,
    ts              INTEGER NOT NULL,
    channel_id      INTEGER NOT NULL,
    frequency_mhz   REAL,
    modulation      TEXT    NOT NULL DEFAULT '',
    multiplex       TEXT    NOT NULL DEFAULT '',
    power_dbmv      REAL
);
CREATE INDEX idx_us_channel_ts ON us_channels(channel_id, ts);
CREATE INDEX idx_us_reading ON us_channels(reading_id);

-- Interruptions de connectivite.
--
-- La sonde tourne bien plus souvent que le releve DOCSIS mais n'ecrit qu'au
-- changement d'etat : une ligne stable ne produit rien, une coupure produit
-- une ligne ouverte puis fermee. C'est ce qui permet de dater une interruption
-- a la minute sans stocker une mesure par minute.
CREATE TABLE link_events (
    id         INTEGER PRIMARY KEY,
    started_at INTEGER NOT NULL,
    ended_at   INTEGER,                 -- NULL tant que l'interruption dure
    kind       TEXT    NOT NULL,        -- 'unreachable' | 'no_sync'
    detail     TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX idx_link_started ON link_events(started_at);

-- Reglages, en cle/valeur.
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
