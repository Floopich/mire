-- Temperatures journalieres du lieu choisi pour la meteo, une ligne par jour
-- local (AAAA-MM-JJ). Rapprochees de la tendance de la ligne.
CREATE TABLE IF NOT EXISTS weather_daily (
    day   TEXT PRIMARY KEY,
    tmin  REAL,
    tmax  REAL,
    tmean REAL
);
