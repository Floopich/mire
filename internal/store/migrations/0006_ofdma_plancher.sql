-- La declaration « OFDMA en basse modulation par conception » devient une
-- modulation habituelle explicite. L'ancien interrupteur tolerait le 16QAM
-- et le 32QAM et alertait en dessous : 16QAM en est l'equivalent.
INSERT INTO settings(key, value)
    SELECT 'reglage.analyse.ofdma_plancher', '16' FROM settings
    WHERE key = 'reglage.analyse.ofdma_basse' AND value = 'true'
ON CONFLICT(key) DO NOTHING;
DELETE FROM settings WHERE key = 'reglage.analyse.ofdma_basse';
