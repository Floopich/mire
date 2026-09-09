"""Compensation legale belge : candidats detectes et calcul du bareme."""

from __future__ import annotations

import logging
import time

from flask import Blueprint, jsonify, request

from app.modules.connection_monitor.storage import ConnectionMonitorStorage
from app.web import get_config_manager, get_storage, require_auth

from . import rules

bp = Blueprint("be_compensation_module", __name__)
log = logging.getLogger("mire.be_compensation")

_MAX_WINDOW_DAYS = 400


def _storage():
    core = get_storage()
    return ConnectionMonitorStorage(core.db_path) if core else None


def _monthly_fee() -> float | None:
    cfg = get_config_manager()
    if not cfg:
        return None
    try:
        value = float(cfg.get("monthly_fee_eur", 0) or 0)
    except (TypeError, ValueError):
        return None
    return value if value > 0 else None


def _positive_int(raw, default):
    try:
        value = int(raw)
    except (TypeError, ValueError):
        return default
    return value if value > 0 else default


@bp.route("/api/be_compensation/candidates")
@require_auth
def candidates():
    """Interruptions detectees qui atteignent le seuil legal de 8 heures.

    Les durees renvoyees sont OBSERVEES. La duree legale court a partir de la
    connaissance de l'operateur : tant que l'utilisateur n'a pas fourni
    l'horodatage de son signalement, `qualified` reste faux et le montant est
    presente comme une borne haute, pas comme un du.
    """
    store = _storage()
    if store is None:
        return jsonify({"error": "storage unavailable"}), 503

    days = min(_positive_int(request.args.get("days"), 90), _MAX_WINDOW_DAYS)
    now = time.time()
    start = now - days * 86400
    fee = _monthly_fee()

    results = []
    for target in store.get_targets():
        for outage in store.get_outages(target["id"], start=start, end=now):
            observed = rules.counted_duration(
                outage["start"], outage.get("end"), None, now
            )
            if observed < rules.THRESHOLD_SECONDS:
                continue
            computed = rules.compute(observed, fee)
            entry = computed.as_dict()
            entry.update({
                "target_id": target["id"],
                "target_label": target.get("label") or target.get("host", ""),
                "detected_start": outage["start"],
                "detected_end": outage.get("end"),
                "observed_seconds": round(observed, 1),
                "ongoing": outage.get("end") is None,
                "qualified": False,
            })
            results.append(entry)

    results.sort(key=lambda item: item["detected_start"], reverse=True)
    return jsonify({
        "candidates": results,
        "window_days": days,
        "monthly_fee_eur": fee,
        "threshold_hours": rules.THRESHOLD_SECONDS // 3600,
    })


@bp.route("/api/be_compensation/compute", methods=["POST"])
@require_auth
def compute():
    """Calcule le du sur une duree legale fournie par l'utilisateur."""
    payload = request.get_json(silent=True) or {}
    try:
        hours = float(payload.get("counted_hours", 0))
    except (TypeError, ValueError):
        return jsonify({"error": "counted_hours must be a number"}), 400
    if hours < 0 or hours > 24 * _MAX_WINDOW_DAYS:
        return jsonify({"error": "counted_hours out of range"}), 400

    force_majeure = bool(payload.get("force_majeure"))
    fee = _monthly_fee()
    result = rules.compute(hours * 3600, fee, force_majeure=force_majeure)
    body = result.as_dict()
    body["monthly_fee_eur"] = fee
    return jsonify(body)
