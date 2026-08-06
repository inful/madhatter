// Login page — auto-dismisses .notification elements five seconds
// after the page becomes interactive.
//
// Why an external file: the page's strict CSP (`script-src 'self'`)
// blocks inline <script> blocks. Vendoring the script under /static/
// alongside the other third-party assets (htmx, bulma, fontawesome)
// is the supported way to run page-specific JS — see
// security_headers.go.
(function () {
    function autoDismiss() {
        var notifications = document.querySelectorAll('.notification');
        notifications.forEach(function (notification) {
            setTimeout(function () {
                if (notification.parentElement) {
                    notification.style.transition = 'opacity 0.5s';
                    notification.style.opacity = '0';
                    setTimeout(function () {
                        if (notification.parentElement) {
                            notification.remove();
                        }
                    }, 500);
                }
            }, 5000);
        });
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', autoDismiss);
    } else {
        autoDismiss();
    }
})();
