"""Parcours de plainte belge : operateur puis Service de mediation.

Sources :
- https://www.mediateurtelecom.be/introduire-une-plainte/comment-introduire-une-plainte/
- https://www.mediateurtelecom.be/introduire-une-plainte/reglement-de-procedure/
- https://www.ibpt.be/consommateurs/plaintes

Le service de mediation est une instance de recours instituee aupres de
l'IBPT par la loi du 21 mars 1991, entite qualifiee au sens du livre XVI du
Code de droit economique. Il ne se substitue pas au service clientele de
l'operateur.

Ce module traite la PROCEDURE. Il ne calcule aucun montant : pour une
interruption totale de 8 h ou plus, voir be_compensation.
"""

from __future__ import annotations

# Delai de traitement annonce, en jours calendrier, prolongeable une fois.
HANDLING_DAYS = 90

# Anciennete maximale de la plainte deposee chez l'operateur.
MAX_AGE_DAYS = 365

STEPS = (
    "contact_operator",
    "keep_written_trace",
    "wait_operator_answer",
    "file_written_complaint",
    "annex_prior_request",
    "await_outcome",
)

# Motifs d'irrecevabilite. Chaque cle correspond a une question posee a
# l'utilisateur ; True signifie que la plainte est bloquee.
BLOCKERS = (
    "no_prior_contact",
    "older_than_one_year",
    "court_case_pending",
)


def assess(answers: dict) -> dict:
    """Evalue la recevabilite a partir des reponses de l'utilisateur.

    Ne tranche rien : le service de mediation examine lui-meme la recevabilite.
    Renvoie les motifs de blocage connus pour eviter un depot voue a l'echec.
    """
    blocking = [key for key in BLOCKERS if bool(answers.get(key))]
    written = bool(answers.get("complaint_is_written"))
    if not written:
        blocking.append("not_written")
    return {
        "admissible": not blocking,
        "blockers": blocking,
        "steps": list(STEPS),
        "handling_days": HANDLING_DAYS,
        "max_age_days": MAX_AGE_DAYS,
    }
