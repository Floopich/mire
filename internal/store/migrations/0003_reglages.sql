-- Les reglages de la page Reglages sont prefixes « reglage. » : la table
-- settings porte aussi des donnees internes (empreinte du mot de passe...).
UPDATE settings SET key = 'reglage.analyse.ofdma_basse' WHERE key = 'ofdma_low_qam_expected';
