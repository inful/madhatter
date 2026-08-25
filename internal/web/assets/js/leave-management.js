// Leave Management page modal — opens / closes the edit modal and
// wires the Esc-to-close listener.
//
// Why an external file: the page's strict CSP (`script-src 'self'`)
// blocks inline <script> blocks. Vendoring the script under /static/
// alongside the other third-party assets (htmx, bulma, fontawesome)
// is the supported way to run page-specific JS — see
// security_headers.go.
//
// Behaviour:
//   - showEditModal fills the modal form with the row's id / member /
//     dates / type and flips it visible (Bulma .is-active class).
//   - closeEditModal hides the modal.
//   - One Esc listener is attached at module-load time and guarded by
//     a window-scoped flag so re-renders (e.g. via HTMX) don't stack
//     duplicate handlers.
(function () {
    function showEditModal(id, memberID, startDate, endDate, leaveType) {
        var modal = document.getElementById('editModal');
        var form = document.getElementById('editForm');
        var memberSelect = document.getElementById('editMemberID');
        var startDateInput = document.getElementById('editStartDate');
        var endDateInput = document.getElementById('editEndDate');
        var leaveTypeSelect = document.getElementById('editLeaveType');

        form.action = '/leave/' + id + '/edit';
        memberSelect.value = memberID;
        startDateInput.value = startDate;
        endDateInput.value = endDate;
        // Default to plain leave if the row predates the leave_type
        // field (older rows stored no leave_type value, but the server
        // falls back to the existing row value when the form omits it).
        leaveTypeSelect.value = leaveType || 'leave';

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

    // Delegated handler for the data-edit-leave action — the per-row
    // Edit button carries the row's id/member/start/end/type as
    // data-* attrs (the CSP-safe replacement for the old
    // onclick='showEditModal(...)' attribute, which the page's strict
    // script-src CSP blocks).
    document.addEventListener('click', function (event) {
        var target = event.target.closest('[data-edit-leave]');
        if (!target) {
            return;
        }
        showEditModal(
            target.dataset.id,
            target.dataset.memberId,
            target.dataset.startDate,
            target.dataset.endDate,
            target.dataset.leaveType
        );
    });

    if (!window._leaveManagementEscListener) {
        window._leaveManagementEscListener = function (event) {
            if (event.key === 'Escape') {
                closeEditModal();
            }
        };
        document.addEventListener('keydown', window._leaveManagementEscListener);
    }
})();
