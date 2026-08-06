// Team page edit modal — opens / closes the admin edit-team-member
// modal and wires the Esc-to-close listener.
//
// Why an external file: the page's strict CSP (`script-src 'self'`)
// blocks inline <script> blocks. Vendoring the script under /static/
// alongside the other third-party assets (htmx, bulma, fontawesome)
// is the supported way to run page-specific JS — see
// security_headers.go.
//
// Behaviour:
//   - showEditModal fills the modal form with the row's id / name /
//     email and flips it visible (Bulma .is-active class).
//   - closeEditModal hides the modal.
//   - One Esc listener is attached at module-load time and guarded by
//     a window-scoped flag so re-renders don't stack duplicate
//     handlers.
(function () {
    function showEditModal(id, name, email) {
        var modal = document.getElementById('editModal');
        var form = document.getElementById('editForm');
        var nameInput = document.getElementById('editName');
        var emailInput = document.getElementById('editEmail');

        form.action = '/team/' + id + '/edit';
        nameInput.value = name;
        emailInput.value = email;

        modal.classList.add('is-active');
    }

    function closeEditModal() {
        var modal = document.getElementById('editModal');
        if (modal) {
            modal.classList.remove('is-active');
        }
    }

    window.showEditModal = showEditModal;
    window.closeEditModal = closeEditModal;

    if (!window._teamEditModalEscListener) {
        window._teamEditModalEscListener = function (event) {
            if (event.key === 'Escape') {
                closeEditModal();
            }
        };
        document.addEventListener('keydown', window._teamEditModalEscListener);
    }
})();
