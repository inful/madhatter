// Quick Actions dropdown — toggles Bulma's `is-active` class on the
// wrapper to show/hide the .dropdown-menu. The menu ships closed (SSR)
// so the dashboard loads with the actions collapsed; the user opens it
// on demand by clicking the trigger.
//
// Why an external file: the page's strict CSP (`script-src 'self'`) blocks
// inline <script> blocks. Vendoring the script under /static/ alongside
// the other third-party assets (htmx, bulma, fontawesome) is the
// supported way to run page-specific JS — see security_headers.go.
//
// Behaviour:
//   - Click the trigger → toggle open/closed. stopPropagation keeps the
//     document-level close handler from immediately closing the menu the
//     click just opened.
//   - Click anywhere outside the dropdown wrapper → close.
//   - Press Escape → close. The window-scoped flag matches the pattern
//     used by leave-management.js's modal Esc listener so duplicate Esc
//     handlers don't stack if the dashboard is ever re-rendered via
//     HTMX.
(function () {
    var dropdown = document.getElementById('quickActionsDropdown');
    var trigger = document.getElementById('quickActionsTrigger');
    if (!dropdown || !trigger) {
        return;
    }

    function setOpen(open) {
        dropdown.classList.toggle('is-active', open);
        trigger.setAttribute('aria-expanded', open ? 'true' : 'false');
    }

    trigger.addEventListener('click', function (event) {
        event.stopPropagation();
        setOpen(!dropdown.classList.contains('is-active'));
    });

    document.addEventListener('click', function (event) {
        if (!dropdown.contains(event.target)) {
            setOpen(false);
        }
    });

    if (!window._quickActionsDropdownEscListener) {
        window._quickActionsDropdownEscListener = function (event) {
            if (event.key === 'Escape') {
                setOpen(false);
            }
        };
        document.addEventListener('keydown', window._quickActionsDropdownEscListener);
    }
})();
