-- Gigue de la sonde, par tranche : variation moyenne de la latence d'une
-- tentative reussie a la suivante, en millisecondes, et le nombre de paires
-- mesurees, qui pondere la moyenne quand une tranche se complete. Table a
-- part plutot qu'une colonne : la migration reste rejouable.
CREATE TABLE IF NOT EXISTS probe_jitter (
    ts      INTEGER PRIMARY KEY,
    avg_ms  REAL    NOT NULL,
    pairs   INTEGER NOT NULL
);
