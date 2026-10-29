-- Le modem publie une ligne par recepteur, meme sans canal : identifiant nul
-- ou aberrant. Ces lignes etaient enregistrees comme des canaux. Un canal
-- DOCSIS porte un identifiant de 1 a 255 : le reste est retire.
DELETE FROM ds_channels WHERE channel_id NOT BETWEEN 1 AND 255;
DELETE FROM us_channels WHERE channel_id NOT BETWEEN 1 AND 255;
