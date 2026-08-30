/* ═══ Mire modal helpers ═══ */
(function() {
    'use strict';

    var confirmState = null;

    function getDialog(idOrEl) {
        if (!idOrEl) return null;
        if (typeof idOrEl === 'string') {
            return document.getElementById(idOrEl.replace(/^#/, ''));
        }
        return idOrEl;
    }

    function prepareDialog(dialog) {
        if (!dialog || dialog.dataset.mireDialogReady === 'true') return;
        dialog.dataset.mireDialogReady = 'true';
        dialog.addEventListener('cancel', function(event) {
            if (dialog.getAttribute('data-modal-dismissible') === 'false') {
                event.preventDefault();
            }
        });
        dialog.addEventListener('close', function() {
            dialog.classList.remove('open');
        });
    }

    function openModal(idOrEl, options) {
        var dialog = getDialog(idOrEl);
        if (!dialog) return null;
        var opts = options || {};
        if (opts.labelledBy && !dialog.getAttribute('aria-labelledby')) {
            dialog.setAttribute('aria-labelledby', opts.labelledBy);
        }
        if (opts.dismissible === false) {
            dialog.setAttribute('data-modal-dismissible', 'false');
        }
        prepareDialog(dialog);
        if (opts.opener && opts.opener !== document.activeElement && typeof opts.opener.focus === 'function') {
            opts.opener.focus({preventScroll: true});
        }
        if (!dialog.open) dialog.showModal();
        dialog.classList.add('open');
        return dialog;
    }

    function closeModal(idOrEl) {
        var dialog = getDialog(idOrEl);
        if (!dialog) return;
        dialog.classList.remove('open');
        if (dialog.open) dialog.close();
    }

    document.addEventListener('click', function(event) {
        var dialog = event.target;
        if (!dialog || dialog.tagName !== 'DIALOG' || !dialog.open) return;
        if (dialog.getAttribute('data-modal-dismissible') === 'false') return;
        if (dialog.id === 'mire-confirm-modal' && confirmState) {
            resolveConfirm(false);
        } else {
            closeModal(dialog);
        }
    });

    function createConfirmModal() {
        var existing = document.getElementById('mire-confirm-modal');
        if (existing) return existing;
        var dialog = document.createElement('dialog');
        dialog.id = 'mire-confirm-modal';
        dialog.className = 'modal-overlay mire-confirm-overlay';
        dialog.setAttribute('role', 'dialog');
        dialog.setAttribute('aria-modal', 'true');
        dialog.setAttribute('aria-labelledby', 'mire-confirm-title');
        dialog.innerHTML = [
            '<div class="modal mire-confirm-modal" style="max-width:520px;">',
            '  <div class="modal-header">',
            '    <h2 id="mire-confirm-title"></h2>',
            '    <button type="button" class="modal-close" id="mire-confirm-x" aria-label="Close">&times;</button>',
            '  </div>',
            '  <div class="modal-body">',
            '    <p id="mire-confirm-message" class="mire-confirm-message"></p>',
            '    <label id="mire-confirm-typed-wrap" class="mire-confirm-typed-wrap" style="display:none;">',
            '      <span id="mire-confirm-typed-label"></span>',
            '      <input id="mire-confirm-typed-input" class="mire-confirm-typed-input" autocomplete="off" spellcheck="false">',
            '    </label>',
            '  </div>',
            '  <div class="incident-modal-footer">',
            '    <div class="modal-footer-left"></div>',
            '    <div style="display:flex; gap:10px;">',
            '      <button type="button" class="btn btn-muted" id="mire-confirm-cancel"></button>',
            '      <button type="button" class="btn btn-accent" id="mire-confirm-ok"></button>',
            '    </div>',
            '  </div>',
            '</div>'
        ].join('');
        dialog.addEventListener('click', function(event) {
            if (event.target === dialog) resolveConfirm(false);
        });
        dialog.addEventListener('cancel', function(event) {
            event.preventDefault();
            resolveConfirm(false);
        });
        document.body.appendChild(dialog);
        document.getElementById('mire-confirm-x').addEventListener('click', function() { resolveConfirm(false); });
        document.getElementById('mire-confirm-cancel').addEventListener('click', function() { resolveConfirm(false); });
        document.getElementById('mire-confirm-ok').addEventListener('click', function() { resolveConfirm(true); });
        var typedInput = document.getElementById('mire-confirm-typed-input');
        typedInput.addEventListener('input', updateConfirmOk);
        typedInput.addEventListener('keydown', function(event) {
            if (event.key !== 'Enter' || !confirmState) return;
            updateConfirmOk();
            if (document.getElementById('mire-confirm-ok').disabled) return;
            event.preventDefault();
            resolveConfirm(true);
        });
        return dialog;
    }

    function updateConfirmOk() {
        if (!confirmState) return;
        var ok = document.getElementById('mire-confirm-ok');
        var typed = document.getElementById('mire-confirm-typed-input');
        ok.disabled = !!confirmState.requireText && typed.value !== confirmState.requireText;
    }

    function resolveConfirm(value) {
        if (!confirmState) return;
        var state = confirmState;
        confirmState = null;
        closeModal('mire-confirm-modal');
        state.resolve(value);
    }

    function mireConfirm(options) {
        var opts = typeof options === 'string' ? {message: options} : (options || {});
        var dialog = createConfirmModal();
        if (confirmState) resolveConfirm(false);
        document.getElementById('mire-confirm-title').textContent = opts.title || (window.T && T.confirm_title) || 'Confirm action';
        document.getElementById('mire-confirm-message').textContent = opts.message || '';
        document.getElementById('mire-confirm-cancel').textContent = opts.cancelText || (window.T && T.cancel) || 'Cancel';
        var ok = document.getElementById('mire-confirm-ok');
        ok.textContent = opts.confirmText || (window.T && T.confirm) || 'Confirm';
        ok.className = 'btn ' + (opts.danger ? 'btn-danger' : 'btn-accent');
        var typedWrap = document.getElementById('mire-confirm-typed-wrap');
        var typedLabel = document.getElementById('mire-confirm-typed-label');
        var typedInput = document.getElementById('mire-confirm-typed-input');
        typedInput.value = '';
        if (opts.requireText) {
            typedWrap.style.display = '';
            typedLabel.textContent = opts.requireLabel || ('Type ' + opts.requireText + ' to confirm');
            typedInput.setAttribute('autofocus', '');
        } else {
            typedWrap.style.display = 'none';
            typedInput.removeAttribute('autofocus');
        }
        return new Promise(function(resolve) {
            confirmState = {resolve: resolve, requireText: opts.requireText || ''};
            updateConfirmOk();
            openModal(dialog);
        });
    }

    window.MireModal = {
        open: openModal,
        close: closeModal,
        confirm: mireConfirm
    };
    window.mireConfirm = mireConfirm;
})();
