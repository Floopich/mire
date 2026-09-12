/* -- Compensation legale belge (article 113/2 LCE) -- */

var _becInitialized = false;
var _becLoading = false;

function _becT(key, fallback) {
    return (window.T && window.T[key]) || fallback;
}

function _becEuro(value) {
    return (Math.round(Number(value) * 100) / 100).toFixed(2) + ' EUR';
}

function _becHours(seconds) {
    return (Number(seconds) / 3600).toFixed(1) + ' h';
}

function _becStamp(epoch) {
    if (!epoch) return '-';
    return new Date(Number(epoch) * 1000).toLocaleString();
}

function _becCell(row, text, className) {
    var cell = document.createElement('td');
    cell.textContent = text;
    if (className) cell.className = className;
    row.appendChild(cell);
}

function _becRenderCandidates(payload) {
    var target = document.getElementById('bec-candidates');
    if (!target) return;
    target.textContent = '';

    if (!payload.candidates || !payload.candidates.length) {
        var empty = document.createElement('p');
        empty.className = 'hint';
        empty.textContent = _becT('mire.be_compensation.none_found', 'No interruption reached the legal threshold.');
        target.appendChild(empty);
        return;
    }

    var table = document.createElement('table');
    table.className = 'data-table';
    var head = document.createElement('tr');
    [
        _becT('mire.be_compensation.col_target', 'Target'),
        _becT('mire.be_compensation.col_start', 'Start'),
        _becT('mire.be_compensation.col_end', 'End'),
        _becT('mire.be_compensation.col_observed', 'Observed'),
        _becT('mire.be_compensation.col_amount', 'Upper bound'),
        _becT('mire.be_compensation.amount_min', 'Lower bound')
    ].forEach(function(label) {
        var cell = document.createElement('th');
        cell.textContent = label;
        head.appendChild(cell);
    });
    table.appendChild(head);

    payload.candidates.forEach(function(entry) {
        var row = document.createElement('tr');
        _becCell(row, entry.target_label || entry.target_id);
        _becCell(row, _becStamp(entry.detected_start));
        _becCell(row, entry.ongoing
            ? _becT('mire.be_compensation.ongoing', 'Ongoing')
            : _becStamp(entry.detected_end));
        _becCell(row, _becHours(entry.observed_seconds));
        _becCell(row, _becEuro(entry.amount_eur));
        _becCell(row, _becEuro(entry.amount_min_eur), 'hint');
        table.appendChild(row);
    });
    target.appendChild(table);

    var caveat = document.createElement('p');
    caveat.className = 'hint';
    caveat.textContent = _becT('mire.be_compensation.not_qualified', '');
    target.appendChild(caveat);
}

function _becLoadCandidates() {
    if (_becLoading) return;
    var target = document.getElementById('bec-candidates');
    var input = document.getElementById('bec-window');
    var days = input ? parseInt(input.value, 10) : 90;
    if (!days || days < 1) days = 90;
    _becLoading = true;
    fetch(mireUrl('/api/be_compensation/candidates?days=' + encodeURIComponent(days)))
        .then(function(response) { return response.json(); })
        .then(function(payload) {
            _becLoading = false;
            if (payload.error) {
                if (target) target.textContent = payload.error;
                return;
            }
            _becRenderCandidates(payload);
        })
        .catch(function(error) {
            _becLoading = false;
            if (target) target.textContent = error.message;
        });
}

function _becLine(parent, label, value, className) {
    var line = document.createElement('p');
    if (className) line.className = className;
    var strong = document.createElement('strong');
    strong.textContent = label + ' ';
    line.appendChild(strong);
    line.appendChild(document.createTextNode(value));
    parent.appendChild(line);
}

function _becRenderResult(payload) {
    var target = document.getElementById('bec-result');
    if (!target) return;
    target.textContent = '';

    if (!payload.eligible) {
        var reasons = {
            below_threshold: 'mire.be_compensation.result_below',
            force_majeure: 'mire.be_compensation.result_force_majeure'
        };
        var key = reasons[payload.reason] || 'mire.be_compensation.result_excluded';
        var blocked = document.createElement('p');
        blocked.className = 'status-warn';
        blocked.textContent = _becT(key, 'No compensation is due.');
        target.appendChild(blocked);
        return;
    }

    _becLine(target, _becT('mire.be_compensation.result_amount', 'Compensation due'),
             _becEuro(payload.amount_eur));
    _becLine(target, _becT('mire.be_compensation.amount_min', 'Lower bound'),
             _becEuro(payload.amount_min_eur), 'hint');
    _becLine(target, _becT('mire.be_compensation.scale_amount', 'Scale amount'),
             _becEuro(payload.scale_amount_eur), 'hint');
    _becLine(target, _becT('mire.be_compensation.periods_started', '24h periods started'),
             String(payload.periods_started), 'hint');

    if (payload.floor_applied) {
        var floor = document.createElement('p');
        floor.className = 'hint';
        floor.textContent = _becT('mire.be_compensation.floor_applied', '');
        target.appendChild(floor);
    }
    if (payload.indexed) {
        var indexed = document.createElement('p');
        indexed.className = 'hint';
        indexed.textContent = _becT('mire.be_compensation.indexed_with', '')
            .replace('{factor}', String(payload.index_factor));
        target.appendChild(indexed);
    }
}

function _becCompute() {
    var target = document.getElementById('bec-result');
    var hoursEl = document.getElementById('bec-hours');
    var exclusionEl = document.getElementById('bec-exclusion');
    var hours = hoursEl ? parseFloat(hoursEl.value) : 0;
    if (isNaN(hours) || hours < 0) hours = 0;
    fetch(mireUrl('/api/be_compensation/compute'), {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({
            counted_hours: hours,
            exclusion: exclusionEl ? exclusionEl.value : ''
        })
    })
        .then(function(response) { return response.json(); })
        .then(function(payload) {
            if (payload.error) {
                if (target) target.textContent = payload.error;
                return;
            }
            _becRenderResult(payload);
        })
        .catch(function(error) {
            if (target) target.textContent = error.message;
        });
}

function initBe_compensation() {
    if (_becInitialized) return;
    _becInitialized = true;
    var button = document.getElementById('bec-compute');
    var windowInput = document.getElementById('bec-window');
    if (button) button.addEventListener('click', _becCompute);
    if (windowInput) windowInput.addEventListener('change', _becLoadCandidates);
    _becLoadCandidates();
}
