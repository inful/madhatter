// Common interactive helpers used by multiple pages. Loaded once from
// base.html next to htmx so every page gets the same behaviour.
//
// Why an external file: the page's strict CSP ('script-src \\'self\\'') blocks
// inline <script> blocks AND inline onclick= event-handler attributes. The
// pattern in this file is the progressive-enhancement replacement for
// every onclick= the codebase used to carry: the template renders a
// data-* attribute that captures the parameter, the delegated listener
// here dispatches based on it. See commit 2b58817 for context on why
// the inline-onclick pattern was CSP-dead.
//
// Behaviour:
//   - data-close-modal: any element with this attribute hides the open
//     Bulma modal by removing the .is-active class. Used by the
//     modal-background click, the X close button, and the Cancel
//     button across team.html and leave_management.html.
//   - data-dismiss-notification: any element with this attribute
//     removes its .notification parent. Used by the login page's
//     notification close button (and would be reusable for any
//     future .notification surfaces).
//   - data-confirm on a <form>: any submit within a form carrying
//     this attribute is gated through window.confirm(); a cancel
//     prevents the submission. Listening at the form level (not on
//     the button) means any submit path through the form is gated,
//     including Enter-in-text-field and future programmatic
//     submissions. Used by wfh_purge, wfh_manage, wfh_list, swaps.
(function () {
    // Delegated click handler: close-modal and dismiss-notification.
    // Using closest() means the attribute can sit on the button OR on
    // a wrapping element (e.g. the modal-background div) and the
    // dispatch still fires when the click target is a child node.
    document.addEventListener('click', function (event) {
        var closeTarget = event.target.closest('[data-close-modal]');
        if (closeTarget) {
            var modal = document.querySelector('.modal.is-active');
            if (modal) {
                modal.classList.remove('is-active');
            }
            return;
        }

        var dismissTarget = event.target.closest('[data-dismiss-notification]');
        if (dismissTarget && dismissTarget.parentElement) {
            dismissTarget.parentElement.remove();
        }
    });

    // Delegated submit handler: data-confirm on a form gates any submit
    // through window.confirm(). preventDefault on the submit event stops
    // the form from POSTing if the user clicks Cancel.
    document.addEventListener('submit', function (event) {
        var form = event.target;
        if (form && form.dataset && form.dataset.confirm) {
            if (!window.confirm(form.dataset.confirm)) {
                event.preventDefault();
            }
        }
    });
})();
