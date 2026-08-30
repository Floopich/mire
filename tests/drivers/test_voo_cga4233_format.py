"""Parsing contracts for the voo_cga4233_json format profile.

Two fixtures, two roles.

``PAYLOAD`` is a small synthetic payload. It pins the parsing logic — field
mapping, sign conventions, ErrTbl positional alignment — and stays readable
inside the test file.

``REAL_CAPTURE`` is a verbatim capture from a Technicolor CGA4233 on VOO
firmware in bridge mode, taken 2026-08-30 (see tests/drivers/fixtures/). It is
what proves the field names match what the firmware actually emits. The capture
carries no MAC address, serial number or subscriber identifier: the endpoint
returns DOCSIS channel tables only.
"""

import json
from pathlib import Path

import pytest

from app.drivers.formats.voo import parse_voo_cga4233_json

REAL_CAPTURE = json.loads(
    (Path(__file__).parent / "fixtures" / "cga4233_voo_bridge.json").read_text(
        encoding="utf-8"
    )
)


PAYLOAD = {
    "data": {
        "DSTbl": [
            {
                "ChannelID": "1", "Modulation": "256QAM", "Frequency": "402",
                "PowerLevel": "2.5", "SNRLevel": "38.6",
                "Correcteds": "120", "Uncorrectables": "3",
            },
            {
                "ChannelID": "2", "Modulation": "256QAM", "Frequency": "410",
                "PowerLevel": "-1.5", "SNRLevel": "37.0",
                "Correcteds": "0", "Uncorrectables": "0",
            },
        ],
        "exDSTbl": [
            {
                "ChannelID": "33", "CentralFrequency": "722",
                "PowerLevel": "-2.6", "SNRLevel": "40.1",
                "FFT": "256-qam/1024-qam",
            },
        ],
        "USTbl": [
            {
                "ChannelID": "1", "Modulation": "64QAM", "Frequency": "39.4",
                "PowerLevel": "44.0", "SymbolRate": "5120000",
            },
        ],
        "exUSTbl": [
            {"ChannelID": "9", "CentralFrequency": "29", "PowerLevel": "45.5", "FFT": "64QAM"},
        ],
        # ErrTbl is positional: 2 rows mirror DSTbl, the 3rd carries OFDM counters.
        "ErrTbl": [
            {"Correcteds": "120", "Uncorrectables": "3"},
            {"Correcteds": "0", "Uncorrectables": "0"},
            {"Correcteds": "77", "Uncorrectables": "11"},
        ],
    }
}


@pytest.fixture
def parsed():
    return parse_voo_cga4233_json(PAYLOAD).value


class TestLaneSplit:
    def test_channels_land_in_their_lanes(self, parsed):
        assert len(parsed["channelDs"]["docsis30"]) == 2
        assert len(parsed["channelDs"]["docsis31"]) == 1
        assert len(parsed["channelUs"]["docsis30"]) == 1
        assert len(parsed["channelUs"]["docsis31"]) == 1

    @pytest.mark.parametrize("payload", [None, {}, {"data": None}, {"data": []}])
    def test_missing_or_malformed_payload_yields_empty_lanes(self, payload):
        value = parse_voo_cga4233_json(payload).value
        assert value["channelDs"] == {"docsis30": [], "docsis31": []}
        assert value["channelUs"] == {"docsis30": [], "docsis31": []}


class TestDownstreamScQam:
    def test_field_mapping(self, parsed):
        ch = parsed["channelDs"]["docsis30"][0]
        assert ch["channelID"] == 1
        assert ch["type"] == "256QAM"
        assert ch["frequency"] == "402 MHz"
        assert ch["powerLevel"] == 2.5

    def test_snr_sign_convention(self, parsed):
        """mse is negative, mer positive, both from the single SNRLevel field."""
        ch = parsed["channelDs"]["docsis30"][0]
        assert ch["mse"] == -38.6
        assert ch["mer"] == 38.6

    def test_error_counters_emitted_under_both_spellings(self, parsed):
        ch = parsed["channelDs"]["docsis30"][0]
        assert ch["corrError"] == ch["corrErrors"] == 120
        assert ch["nonCorrError"] == ch["nonCorrErrors"] == 3


class TestDownstreamOfdm:
    def test_type_and_frequency(self, parsed):
        ch = parsed["channelDs"]["docsis31"][0]
        assert ch["channelID"] == 33
        assert ch["type"] == "OFDM"
        assert ch["frequency"] == "722 MHz"

    def test_fft_profiles_go_to_profile_modulation_not_type(self, parsed):
        ch = parsed["channelDs"]["docsis31"][0]
        assert ch["profile_modulation"] == "256-qam/1024-qam"
        assert ch["type"] == "OFDM"

    def test_ofdm_errors_come_from_trailing_errtbl_rows(self, parsed):
        """exDSTbl has no error fields; counters align positionally after DSTbl."""
        ch = parsed["channelDs"]["docsis31"][0]
        assert ch["corrErrors"] == 77
        assert ch["nonCorrErrors"] == 11

    def test_missing_errtbl_rows_leave_counters_none(self):
        payload = {"data": dict(PAYLOAD["data"], ErrTbl=[])}
        ch = parse_voo_cga4233_json(payload).value["channelDs"]["docsis31"][0]
        assert ch["corrErrors"] is None
        assert ch["nonCorrErrors"] is None


class TestUpstream:
    def test_sc_qam_symbol_rate_is_normalized_to_ksym(self, parsed):
        """5120000 sym/s is reported as 5120; values already in ksym stay put."""
        assert parsed["channelUs"]["docsis30"][0]["symbolRate"] == 5120

    def test_sc_qam_small_symbol_rate_is_left_untouched(self):
        rows = [dict(PAYLOAD["data"]["USTbl"][0], SymbolRate="5120")]
        payload = {"data": dict(PAYLOAD["data"], USTbl=rows)}
        assert parse_voo_cga4233_json(payload).value["channelUs"]["docsis30"][0]["symbolRate"] == 5120

    def test_frequency_is_reported_in_mhz(self, parsed):
        """Le firmware envoie une chaine deja suffixee ("466 MHz").

        Verifie sur capture reelle : _mhz() extrait le nombre avant de
        reformater, donc pas de double unite.
        """
        assert parsed["channelDs"]["docsis30"][0]["frequency"] == "402 MHz"

    def test_ofdma_carries_fft_as_modulation(self, parsed):
        ch = parsed["channelUs"]["docsis31"][0]
        assert ch["type"] == "OFDMA"
        assert ch["modulation"] == "64QAM"
        assert ch["frequency"] == "29 MHz"


class TestRealCapture:
    """Caracterisation contre une capture verbatim d un CGA4233 sur VOO.

    Materiel : Technicolor CGA4233, firmware VOO, mode bridge, 2026-08-30.
    Ces tests echouent si une mise a jour du firmware renomme un champ ou
    reorganise les tables — de preference avant qu un rapport parte chez un
    abonne.
    """

    @pytest.fixture
    def parsed(self):
        return parse_voo_cga4233_json(REAL_CAPTURE).value

    def test_lane_counts_match_the_captured_line(self, parsed):
        assert len(parsed["channelDs"]["docsis30"]) == 16
        assert len(parsed["channelDs"]["docsis31"]) == 1
        assert len(parsed["channelUs"]["docsis30"]) == 4
        assert len(parsed["channelUs"]["docsis31"]) == 1

    def test_firmware_frequency_strings_are_not_double_suffixed(self, parsed):
        assert parsed["channelDs"]["docsis30"][0]["frequency"] == "466 MHz"
        assert parsed["channelUs"]["docsis30"][0]["frequency"] == "37 MHz"

    def test_firmware_modulation_spellings_are_normalized(self, parsed):
        # Le firmware ecrit "256-QAM", "32-qam" et "16-qam".
        assert parsed["channelDs"]["docsis30"][0]["type"] == "256QAM"
        assert parsed["channelUs"]["docsis30"][0]["type"] == "32QAM"
        assert parsed["channelUs"]["docsis31"][0]["modulation"] == "16QAM"

    def test_snr_becomes_negative_mse_and_positive_mer(self, parsed):
        channel = parsed["channelDs"]["docsis30"][0]
        assert channel["mse"] == -41.7
        assert channel["mer"] == 41.7

    def test_ofdm_uses_central_frequency_not_start_frequency(self, parsed):
        # StartFrequency vaut 624.724976 MHz, CentralFrequency 706.487488 MHz.
        assert parsed["channelDs"]["docsis31"][0]["frequency"] == "706 MHz"

    def test_errtbl_trailing_row_feeds_the_ofdm_counters(self, parsed):
        # 16 canaux SC-QAM, 17 lignes ErrTbl : la derniere porte l OFDM.
        assert len(REAL_CAPTURE["data"]["ErrTbl"]) == 17
        ofdm = parsed["channelDs"]["docsis31"][0]
        assert ofdm["corrErrors"] == 1762409857
        assert ofdm["nonCorrErrors"] == 7744533

    def test_scqam_error_counters_align_row_by_row(self, parsed):
        by_id = {c["channelID"]: c for c in parsed["channelDs"]["docsis30"]}
        assert by_id[1]["nonCorrErrors"] == 53
        assert by_id[2]["nonCorrErrors"] == 3
        assert by_id[4]["nonCorrErrors"] == 0

    def test_ofdm_profile_list_lands_in_profile_modulation(self, parsed):
        ofdm = parsed["channelDs"]["docsis31"][0]
        assert ofdm["type"] == "OFDM"
        assert ofdm["profile_modulation"] == "256-qam/1024-qam/2048-qam/4096-qam"
