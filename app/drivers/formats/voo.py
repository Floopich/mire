"""Payload format profile for the Technicolor CGA4233 on VOO (Belgium) firmware.

Pure parsing: no transport, no authentication, no clock. The driver fetches
``/api/v1/modem/exUSTbl,exDSTbl,USTbl,DSTbl,ErrTbl`` and hands the decoded JSON
here.

Conventions follow the upstream Vodafone Station profile: ``mse`` negative,
``mer`` positive, ``type`` carries the modulation, frequencies as integer MHz.

Verified against firmware CGA4233VOO, bridge mode, 2026-08.
"""

from __future__ import annotations

from ...types import DocsisDataFritz, RawChannel
from .contract import ParseResult, docsis_split
from .primitives import normalize_modulation, parse_number


def parse_vodafone_number(value):
    """Tolere les firmwares qui renvoient des nombres au lieu de chaines."""
    return parse_number(value) if isinstance(value, str) else float(value or 0)


def _mhz(value) -> str:
    """Format a unit-bearing frequency string using the integer-MHz convention."""
    freq = parse_number(str(value or ""))
    return f"{int(freq)} MHz" if freq else ""


def _to_int(value, default=None):
    try:
        return int(str(value).strip())
    except (TypeError, ValueError):
        return default


def _errors(source: dict) -> dict:
    """Error counters under both spellings.

    The Vodafone profile writes corrError/nonCorrError; types.RawChannel
    declares corrErrors/nonCorrErrors. Emit both so either consumer resolves.
    """
    corrected = _to_int(source.get("Correcteds"))
    uncorrected = _to_int(source.get("Uncorrectables"))
    return {
        "corrError": corrected, "nonCorrError": uncorrected,
        "corrErrors": corrected, "nonCorrErrors": uncorrected,
    }


def parse_ds_scqam(ch: dict) -> RawChannel:
    snr = abs(parse_vodafone_number(ch.get("SNRLevel", "0")))
    return {
        "channelID": int(parse_vodafone_number(ch.get("ChannelID", "0"))),
        "type": normalize_modulation(ch.get("Modulation", "")),
        "frequency": _mhz(ch.get("Frequency")),
        "powerLevel": parse_vodafone_number(ch.get("PowerLevel", "0")),
        "mse": -snr if snr else None,
        "mer": snr if snr else None,
        "latency": 0,
        **_errors(ch),
    }


def parse_ds_ofdm(ch: dict, errors: dict) -> RawChannel:
    snr = abs(parse_vodafone_number(ch.get("SNRLevel", "0")))
    channel: RawChannel = {
        "channelID": int(parse_vodafone_number(ch.get("ChannelID", "0"))),
        "type": "OFDM",
        "frequency": _mhz(ch.get("CentralFrequency")),
        "powerLevel": parse_vodafone_number(ch.get("PowerLevel", "0")),
        "mse": -snr if snr else None,
        "mer": snr if snr else None,
        "latency": 0,
        **_errors(errors),
    }
    # FFT lists every active profile ("256-qam/1024-qam/...") -- surfaced as
    # profile modulation rather than forced into the single type field.
    profiles = (ch.get("FFT") or "").strip()
    if profiles:
        channel["profile_modulation"] = profiles
    return channel


def parse_us_scqam(ch: dict) -> RawChannel:
    return {
        "channelID": int(parse_vodafone_number(ch.get("ChannelID", "0"))),
        "type": normalize_modulation(ch.get("Modulation", "")),
        "frequency": _mhz(ch.get("Frequency")),
        "powerLevel": parse_vodafone_number(ch.get("PowerLevel", "0")),
        "multiplex": "",
        "symbolRate": (lambda v: v // 1000 if v and v > 20000 else v)(_to_int(ch.get("SymbolRate"))),
    }


def parse_us_ofdma(ch: dict) -> RawChannel:
    modulation = normalize_modulation(ch.get("FFT", ""))
    return {
        "channelID": int(parse_vodafone_number(ch.get("ChannelID", "0"))),
        "type": "OFDMA",
        "modulation": modulation or "OFDMA",
        "frequency": _mhz(ch.get("CentralFrequency")),
        "powerLevel": parse_vodafone_number(ch.get("PowerLevel", "0")),
        "multiplex": "",
    }


def parse_voo_cga4233_json(payload: dict | None) -> ParseResult[DocsisDataFritz]:
    """Turn one decoded modem JSON payload into the four-lane DOCSIS result."""
    payload = payload or {}
    data = payload.get("data", payload)
    if not isinstance(data, dict):
        return ParseResult(docsis_split([], [], [], []))

    ds_scqam = data.get("DSTbl") or []
    ds_ofdm = data.get("exDSTbl") or []
    us_scqam = data.get("USTbl") or []
    us_ofdma = data.get("exUSTbl") or []
    err_rows = data.get("ErrTbl") or []

    # ErrTbl is positional: leading rows mirror DSTbl, trailing rows carry the
    # OFDM counters (exDSTbl has no error fields of its own).
    ofdm_errors = err_rows[len(ds_scqam):]

    ds30 = [parse_ds_scqam(c) for c in ds_scqam]
    ds31 = [
        parse_ds_ofdm(c, ofdm_errors[i] if i < len(ofdm_errors) else {})
        for i, c in enumerate(ds_ofdm)
    ]
    us30 = [parse_us_scqam(c) for c in us_scqam]
    us31 = [parse_us_ofdma(c) for c in us_ofdma]

    return ParseResult(docsis_split(ds30, ds31, us30, us31))
