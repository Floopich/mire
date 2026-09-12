/* -- Parcours de plainte et mediation (Belgique) -- */

var _bemInitialized = false;

function _bemT(key, fallback) {
    return (window.T && window.T[key]) || fallback;
}

function _bemAnswers() {
    function checked(id) {
        var el = document.getElementById(id);
        return !!(el && el.checked);
    }
    return {
        no_prior_contact: checked('bem-no-contact'),
        older_than_one_year: checked('bem-older'),
        court_case_pending: checked('bem-court'),
        complaint_is_written: checked('bem-written')
    };
}

function _bemRender(payload) {
    var target = document.getElementById('bem-result');
    if (!target) return;
    target.textContent = '';

    var verdict = document.createElement('p');
    verdict.className = payload.admissible ? 'status-ok' : 'status-warn';
    verdict.textContent = payload.admissible
        ? _bemT('mire.be_mediation.admissible', 'No known blocker.')
        : _bemT('mire.be_mediation.not_admissible', 'A blocker would stop this complaint.');
    target.appendChild(verdict);

    if (payload.blockers && payload.blockers.length) {
        var list = document.createElement('ul');
        payload.blockers.forEach(function(key) {
            var item = document.createElement('li');
            item.textContent = _bemT('mire.be_mediation.blocker.' + key, key);
            list.appendChild(item);
        });
        target.appendChild(list);
    }

    if (payload.handling_days) {
        var delay = document.createElement('p');
        delay.className = 'hint';
        delay.textContent = String(payload.handling_days) + ' ' +
            _bemT('mire.be_mediation.handling_days', 'calendar days, extendable once.');
        target.appendChild(delay);
    }
}

function _bemAssess() {
    var target = document.getElementById('bem-result');
    fetch(mireUrl('/api/be_mediation/assess'), {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify(_bemAnswers())
    })
        .then(function(response) { return response.json(); })
        .then(function(payload) {
            if (payload.error) {
                if (target) target.textContent = payload.error;
                return;
            }
            _bemRender(payload);
        })
        .catch(function(error) {
            if (target) target.textContent = error.message;
        });
}

function initBe_mediation() {
    if (_bemInitialized) return;
    _bemInitialized = true;
    ['bem-no-contact', 'bem-older', 'bem-court', 'bem-written'].forEach(function(id) {
        var el = document.getElementById(id);
        if (el) el.addEventListener('change', _bemAssess);
    });
    _bemAssess();
}
