// Calendar page copy-to-clipboard helper — backs the "Copy" buttons
// next to each subscription URL. Shows a confirmation notification
// for two seconds after a successful copy.
//
// Why an external file: the page's strict CSP (`script-src 'self'`)
// blocks inline <script> blocks. Vendoring the script under /static/
// alongside the other third-party assets (htmx, bulma, fontawesome)
// is the supported way to run page-specific JS — see
// security_headers.go.
(function () {
    // Delegated handler for the data-copy-url action — the Copy buttons
    // next to each subscription URL carry the source box id in a data-*
    // attribute (the CSP-safe replacement for the old
    // onclick="copyTextFromBox('box-id')" attribute, which the page's
    // strict script-src CSP blocks). window.copyTextFromBox is still
    // exposed for any callers that need the imperative form.
    document.addEventListener('click', function (event) {
        var target = event.target.closest('[data-copy-url]');
        if (!target) {
            return;
        }
        window.copyTextFromBox(target.dataset.copyUrl);
    });

    window.copyTextFromBox = function (boxId) {
        var urlBox = document.getElementById(boxId);
        if (!urlBox) {
            return;
        }
        var urlText = urlBox.dataset.url || '';
        var textarea = document.createElement('textarea');
        textarea.value = urlText;
        textarea.style.position = 'fixed';
        textarea.style.opacity = '0';
        document.body.appendChild(textarea);
        textarea.select();

        try {
            document.execCommand('copy');
            var notification = document.getElementById('copy-notification');
            if (notification) {
                notification.style.display = 'block';
                setTimeout(function () {
                    notification.style.display = 'none';
                }, 2000);
            }
        } finally {
            document.body.removeChild(textarea);
        }
    };
})();
