// Day-rollover wrapper (#day-rollover): forces a page reload
// when the user returns to a tab on a new day. Without this,
// a tab left open overnight keeps showing yesterday's HAT
// banner, leaves, schedule matrix, and birthday copy until
// the user does a manual refresh.
//
// Contract:
//   - At load time, capture today's date from the
//     `<meta name="current-day">` meta tag. The tag carries a
//     YYYY-MM-DD string rendered server-side. The capture is
//     stable: every time the same meta tag is re-read (after a
//     soft reload, after a back/forward cache restore, etc.) the
//     captured value is the same string the server produced.
//   - Register a `visibilitychange` listener on the document.
//     When the document becomes visible:
//       1. Read the current browser date as a YYYY-MM-DD string
//          (using the same YYYY-MM-DD format the meta tag uses,
//          so the comparison is a plain string equality).
//       2. If the captured day differs from today's, call
//          `location.reload()`. The reload re-runs the page
//          server-side, which re-runs the dashboard's
//          loadDashboardData with the now-current `now`, and
//          the dashboard reflects the new day on the next paint.
//       3. If the captured day matches today's, do nothing —
//          the user is just toggling tabs on the same day, no
//          reload is necessary.
//
// Why a page reload (vs. an HTMX partial fetch):
//   - The dashboard has client-side state in dashboard.js
//     (Quick Actions dropdown), scroll position, and in some
//     flows an in-progress form entry. A reload preserves the
//     server-rendered HTML exactly and lets the next click work
//     against the new day's data without partial-state bugs.
//   - The user's request is explicit: when switching to the
//     tab on a new day, show the new day. Reload is the
//     simplest reliable way to honor that.
//
// Why not a `setInterval` polling the server: visibilitychange
// fires when the tab becomes visible (browser tab switch,
// laptop lid open, sleep wake), which is exactly the user's
// trigger. Polling would fire while the tab is in the
// background, waste battery on a hidden tab, and compete with
// the dashboard's other server probes.
//
// Why not `focus` event: focus fires on any focusable element
// when the window regains focus, but tabs that never had
// keyboard focus (e.g. user clicked directly into a form field)
// miss it. visibilitychange is the standard API for "the page
// is now visible to the user" and works in every modern
// browser.
//
// Reduced-motion / accessibility: no DOM mutation, no
// animation. Just a single date comparison and a possible
// page reload. Screen readers see no visual change (the
// reload is a standard navigation event).
//
// Why an external file: the page's strict CSP
// (`script-src 'self'`) blocks inline <script> blocks.
// Vendoring the script under /static/ alongside the other
// third-party assets (htmx, bulma, fontawesome, canvas-confetti)
// is the supported way to run page-specific JS — see
// security_headers.go.
(function () {
    var meta = document.querySelector('meta[name="current-day"]');
    if (!meta) {
        // Page didn't render the current-day meta tag (e.g. an
        // old cached page that predates the feature). Skip
        // silently — the page just won't auto-roll-over.
        return;
    }

    var capturedDay = meta.getAttribute('content');
    if (!capturedDay) {
        return;
    }

    function todayAsString() {
        // The meta tag uses YYYY-MM-DD; new Date().toISOString()
        // returns YYYY-MM-DDTHH:MM:SSZ. Slice to 10 chars to match.
        // toISOString is UTC; that's the same frame the server's
        // Format("2006-01-02") uses for the dashboard (see
        // loadDashboardData in internal/web/dashboard_data.go),
        // so the two strings are always in the same time zone.
        return new Date().toISOString().slice(0, 10);
    }

    function onVisibilityChange() {
        if (document.visibilityState !== 'visible') {
            return;
        }
        if (todayAsString() !== capturedDay) {
            // The captured day is in the past (the user kept
            // the tab open across midnight, or laptop woke from
            // sleep on a new day, or the system clock jumped).
            // Reload so the dashboard reflects the new day.
            window.location.reload();
        }
    }

    document.addEventListener('visibilitychange', onVisibilityChange);
})();
