"""Built-in DOCSIS analyzer threshold profiles.

Threshold profiles are Mire-owned analyzer configuration, not community
module wrappers.  Community modules may still contribute additional threshold
profiles through the module loader, but shipped defaults live here.
"""

from __future__ import annotations

BUILTIN_THRESHOLD_PROFILES: tuple[dict[str, object], ...] = (
    {
        "id": "mire.thresholds_voo",
        "name": "VOO Thresholds",
        "description": "Seuils de signal pour les lignes cable VOO (Belgique)",
        "version": "1.1.0",
        "author": "Mire",
        "minAppVersion": "2026.2",
        "thresholds": {
            "_meta": {
                "region": "Belgium",
                "operator": "VOO",
                "docsis_variant": "eurodocsis",
                "source": "https://forum.voo.be",
                "provenance": "Valeurs empiriques issues du module community.thresholds_voo v1.0.0 (auteur guntherlogy, MIT), non normatives. 1.1.0 ajoute les lignes ofdm de downstream_power et de snr, absentes du module, et precise l attribution de la ligne ofdm de snr.",
                "notes": "Seuils issus des retours de la communaute et des experts VOO. En pratique VOO, la voie retour n'a pas de borne basse significative ; seul un TX excessif pose probleme (normal sous 49 dBmV, 51 est le plafond sur 4 porteuses, les canaux tombent au-dela de 52). Puissances en dBmV, SNR/MER en dB. La plage bonne en voie descendante est elargie a +/-8 dBmV : la pratique VOO considere 8-10 dBmV comme non problematique. La ligne ofdm de downstream_power applique la meme regle VOO que les autres modulations. La ligne ofdm de snr est reprise du profil VFKD amont : ses bornes s appuient sur CableLabs DOCSIS 3.1 PHY CM-SP-PHYv3.1-I08, a l exception du seuil d avertissement de 25.5 dB que DOCSight indique avoir derive lui-meme. VOO ne publie pas de plancher de MER agrege : le MER agrege d un canal OFDM se mesure autrement que le SNR par porteuse, et sans ces lignes un canal OFDM serait juge sur le good_min de 40 dB du 4096QAM.",
            },
            "downstream_power": {
                "_default": "256QAM",
                "64QAM": {"good": [-8.0, 8.0], "warning": [-10.0, 10.0], "critical": [-15.0, 15.0]},
                "256QAM": {"good": [-8.0, 8.0], "warning": [-10.0, 10.0], "critical": [-15.0, 15.0]},
                "1024QAM": {"good": [-8.0, 8.0], "warning": [-10.0, 10.0], "critical": [-15.0, 15.0]},
                "4096QAM": {"good": [-8.0, 8.0], "warning": [-10.0, 10.0], "critical": [-15.0, 15.0]},
                "ofdm": {"good": [-8.0, 8.0], "warning": [-10.0, 10.0], "critical": [-15.0, 15.0]},
            },
            "upstream_power": {
                "_default": "sc_qam",
                "sc_qam": {"good": [30.0, 49.0], "warning": [27.0, 51.0], "critical": [25.0, 52.0]},
                "ofdma": {"good": [30.0, 49.0], "warning": [27.0, 51.0], "critical": [25.0, 52.0]},
            },
            "snr": {
                "_default": "256QAM",
                "64QAM": {"good_min": 27.0, "warning_min": 25.0, "critical_min": 24.0},
                "256QAM": {"good_min": 35.0, "warning_min": 33.0, "critical_min": 30.0},
                "1024QAM": {"good_min": 39.0, "warning_min": 37.0, "critical_min": 36.0},
                "4096QAM": {"good_min": 40.0, "warning_min": 38.0, "critical_min": 36.0},
                "ofdm": {"good_min": 27.0, "warning_min": 25.5, "critical_min": 24.5},
            },
            # TODO(1) : ces bornes visent le SC-QAM. Un canal OFDMA en 16QAM
            # ressort critique alors que c est un fonctionnement normal. Voir TODO.md.
            "upstream_modulation": {
                "critical_max_qam": 4,
                "warning_max_qam": 16,
            },
            "errors": {
                "uncorrectable_pct": {"warning": 1.0, "critical": 3.0},
                "spike_expiry_hours": 48,
            },
        },
    },
)
