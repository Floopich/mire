"""Evidence checklist assembly for the guided evidence journey."""

from __future__ import annotations

from collections.abc import Iterable, Mapping
from datetime import datetime, timezone
from typing import Any

from app.aggregation import source_coverage

PRESENT = "present"
STALE = "stale"
MISSING = "missing"
OPTIONAL = "optional"
NOT_APPLICABLE = "not_applicable"
UNAVAILABLE = "unavailable"

# Generous thresholds. The checklist is guidance, not an SLA monitor.
_STALE_HOURS = {
    "signal": 2,
    "speedtest": 4,
    "latency": 4,
}


def _parse_ts(value: str | None) -> datetime | None:
    if not value:
        return None
    try:
        normalized = value.replace("Z", "+00:00")
        parsed = datetime.fromisoformat(normalized)
        if parsed.tzinfo is None:
            parsed = parsed.replace(tzinfo=timezone.utc)
        return parsed.astimezone(timezone.utc)
    except (TypeError, ValueError):
        return None


def _latest_ts(rows: Iterable[dict[str, Any]], timestamp_key: str = "timestamp") -> str | None:
    return source_coverage(rows, timestamp_key)["last_observed_at"]


def _is_stale(last_ts: str | None, window_end: str | None, hours: int) -> bool:
    last = _parse_ts(last_ts)
    end = _parse_ts(window_end)
    if last is None or end is None:
        return False
    return (end - last).total_seconds() > hours * 3600


def _item(
    key: str,
    status: str,
    count: int = 0,
    last_ts: str | None = None,
    action: dict[str, str] | None = None,
    hint_key: str | None = None,
    sources: list[dict[str, Any]] | None = None,
) -> dict[str, Any]:
    payload = {
        "key": key,
        "status": status,
        "count": count,
        "last_ts": last_ts,
        "action": action or {},
        "label_key": f"mire.evidence.item.{key}.label",
        "hint_key": hint_key or f"mire.evidence.item.{key}.{status}",
    }
    if sources is not None:
        payload["sources"] = sources
    return payload


def _source_rows(timeline: Iterable[dict[str, Any]], *sources: str) -> list[dict[str, Any]]:
    wanted = set(sources)
    return [row for row in timeline if row.get("source") in wanted]


def _row_count(rows: Iterable[dict[str, Any]]) -> int:
    return source_coverage(rows)["count"]


def _evidence_status_values(
    count: int,
    last_ts: str | None,
    *,
    configured: bool = True,
    applicable: bool = True,
    optional_when_unconfigured: bool = True,
    window_end: str | None,
    stale_key: str | None = None,
) -> tuple[str, str | None]:
    if not applicable:
        return NOT_APPLICABLE, None
    if not configured and count == 0:
        return (OPTIONAL if optional_when_unconfigured else MISSING), None
    if count == 0:
        return MISSING, None
    if stale_key and _is_stale(last_ts, window_end, _STALE_HOURS[stale_key]):
        return STALE, last_ts
    return PRESENT, last_ts


def _evidence_status(
    rows: list[dict[str, Any]],
    *,
    configured: bool = True,
    applicable: bool = True,
    optional_when_unconfigured: bool = True,
    window_end: str | None,
    stale_key: str | None = None,
) -> tuple[str, str | None]:
    coverage = source_coverage(rows)
    return _evidence_status_values(
        coverage["count"],
        coverage["last_observed_at"],
        configured=configured,
        applicable=applicable,
        optional_when_unconfigured=optional_when_unconfigured,
        window_end=window_end,
        stale_key=stale_key,
    )


def _latency_item(
    *,
    connection_latency_rows: list[dict[str, Any]],
    connection_monitor_configured: bool,
    window_end: str | None,
) -> dict[str, Any]:
    cm_status, cm_last = _evidence_status(
        connection_latency_rows,
        configured=connection_monitor_configured,
        optional_when_unconfigured=True,
        window_end=window_end,
        stale_key="latency",
    )
    cm_count = _row_count(connection_latency_rows)
    if cm_status in {MISSING, UNAVAILABLE}:
        status = MISSING
    else:
        status = cm_status
    hint_key = "mire.evidence.item.latency.present_cm_only" if status in {PRESENT, STALE} else None
    return _item(
        "latency",
        status,
        cm_count,
        _latest_ts(connection_latency_rows),
        {"view": "connection-monitor"},
        hint_key=hint_key,
        sources=[
            {"key": "connection_monitor", "status": cm_status, "count": cm_count, "last_ts": cm_last},
        ],
    )


def build_checklist(
    window: dict[str, Any],
    *,
    timeline: list[dict[str, Any]],
    journal_entries: list[dict[str, Any]],
    connection_latency_rows: list[dict[str, Any]] | None = None,
    capabilities: dict[str, Any] | None = None,
    snapshot_aggregate: Mapping[str, Any] | None = None,
) -> list[dict[str, Any]]:
    """Build the stateless evidence checklist for an incident or selected window."""
    capabilities = capabilities or {}
    connection_latency_rows = connection_latency_rows or []
    docsis_supported = bool(capabilities.get("docsis_supported", True))
    speedtest_configured = bool(capabilities.get("speedtest_configured", True))
    connection_monitor_configured = bool(capabilities.get("connection_monitor_configured", False))
    window_end = window.get("to")

    signal_rows = _source_rows(timeline, "modem")
    speedtest_rows = _source_rows(timeline, "speedtest")
    event_rows = _source_rows(timeline, "event", "events")

    if snapshot_aggregate is None:
        signal_count = len(signal_rows)
        signal_status, signal_last = _evidence_status(
            signal_rows,
            applicable=docsis_supported,
            window_end=window_end,
            stale_key="signal",
        )
    else:
        signal_count = int(snapshot_aggregate.get("snapshot_count") or 0)
        signal_last = snapshot_aggregate.get("last_observed_at")
        signal_status, signal_last = _evidence_status_values(
            signal_count,
            signal_last,
            applicable=docsis_supported,
            window_end=window_end,
            stale_key="signal",
        )
    speed_status, speed_last = _evidence_status(
        speedtest_rows,
        configured=speedtest_configured,
        optional_when_unconfigured=True,
        window_end=window_end,
        stale_key="speedtest",
    )
    event_status, event_last = _evidence_status(
        event_rows,
        applicable=docsis_supported,
        optional_when_unconfigured=False,
        window_end=window_end,
    )
    journal_status = PRESENT if journal_entries else MISSING
    journal_last = _latest_ts(journal_entries, "date") or _latest_ts(journal_entries, "created_at")

    items = [
        _item("signal", signal_status, signal_count, signal_last, {"view": "correlation"}),
        _item("speedtest", speed_status, len(speedtest_rows), speed_last, {"view": "speedtest"}),
        _latency_item(
            connection_latency_rows=connection_latency_rows,
            connection_monitor_configured=connection_monitor_configured,
            window_end=window_end,
            ),
        _item("events", event_status, len(event_rows), event_last, {"view": "events"}),
        _item("journal", journal_status, len(journal_entries), journal_last, {"view": "journal", "action": "add_note"}),
    ]
    evidence_ready = any(item["status"] in {PRESENT, STALE} for item in items)
    items.append(_item(
        "comparison",
        OPTIONAL,
        action={"view": "comparison"},
        hint_key="mire.evidence.item.comparison.optional",
    ))
    items.append(_item(
        "review",
        OPTIONAL,
        action={"view": "correlation"},
        hint_key="mire.evidence.item.review.optional",
    ))
    items.append(_item(
        "report",
        PRESENT if evidence_ready else MISSING,
        action={"action": "report"},
        hint_key="mire.evidence.item.report.present" if evidence_ready else "mire.evidence.item.report.missing",
    ))
    return items


def summarize_checklist(items: Iterable[dict[str, Any]]) -> dict[str, int]:
    """Return status counts for the checklist payload."""
    summary = {PRESENT: 0, STALE: 0, MISSING: 0, OPTIONAL: 0, NOT_APPLICABLE: 0}
    for item in items:
        status = item.get("status")
        if status in summary:
            summary[status] += 1
    return summary
