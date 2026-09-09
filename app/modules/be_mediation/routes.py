"""Routes du guide de plainte (Service de mediation pour les telecommunications)."""

from __future__ import annotations

import logging

from flask import Blueprint, jsonify, request

from app.web import require_auth

from . import procedure

bp = Blueprint("be_mediation_module", __name__)
log = logging.getLogger("mire.be_mediation")


@bp.route("/api/be_mediation/assess", methods=["POST"])
@require_auth
def assess():
    """Retourne les motifs d'irrecevabilite connus pour un dossier."""
    payload = request.get_json(silent=True) or {}
    if not isinstance(payload, dict):
        return jsonify({"error": "payload must be an object"}), 400
    return jsonify(procedure.assess(payload))


@bp.route("/api/be_mediation/procedure")
@require_auth
def steps():
    """Etapes et delais, sans etat utilisateur."""
    return jsonify({
        "steps": list(procedure.STEPS),
        "blockers": list(procedure.BLOCKERS),
        "handling_days": procedure.HANDLING_DAYS,
        "max_age_days": procedure.MAX_AGE_DAYS,
    })
