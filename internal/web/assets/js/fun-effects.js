// Fun effects for the dashboard — celebratory confetti when the
// entire team is on-site, and gently falling snow through the
// month of December. The trigger flags are rendered into a hidden
// <div id="fun-effects-config"> by the dashboard template; this
// file reads them once on DOMContentLoaded and fires the
// configured effect exactly once per page load.
//
// The two effects use canvas-confetti (vendored at
// /static/js/canvas-confetti.browser.min.js, which exposes a
// global `confetti` function), so the only third-party code path
// is the library itself. The two recipes are adapted from the
// upstream demos (realistic / snow on kirilv.com/canvas-confetti/);
// the modifications are documented inline.
//
// Respecting prefers-reduced-motion: canvas-confetti has a built-in
// `disableForReducedMotion: true` option that no-ops the confetti
// burst when the user has the OS-level reduced-motion preference
// set. The snow effect wraps the whole loop in a manual check
// because the library option only covers individual confetti()
// calls, not the requestAnimationFrame-driven snow storm.
//
// Why an external file: the page's strict CSP (`script-src 'self'`)
// blocks inline <script> blocks. Vendoring the script under /static/
// alongside the other third-party assets (htmx, bulma, fontawesome,
// canvas-confetti) is the supported way to run page-specific JS —
// see security_headers.go.
(function () {
    function init() {
        var cfg = document.getElementById('fun-effects-config');
        if (!cfg) {
            return;
        }
        var wantConfetti = cfg.getAttribute('data-confetti') === 'true';
        var wantSnow = cfg.getAttribute('data-snow') === 'true';

        if (typeof window.confetti !== 'function') {
            // The vendored bundle failed to load (offline deploy,
            // blocked by an upstream CSP mistake, etc.). Fail
            // closed: the dashboard still renders, just without
            // the celebration. Logged so an operator notices.
            if (wantConfetti || wantSnow) {
                console.warn('fun-effects: canvas-confetti global not available; effects skipped');
            }
            return;
        }

        if (wantConfetti) {
            fireFullTeamBurst();
        }
        if (wantSnow && !prefersReducedMotion()) {
            startDecemberSnow();
        }
    }

    // fireFullTeamBurst fires a multi-burst realistic() confetti
    // celebration — four staggered calls with varied spread and
    // scalar so the result looks "natural" rather than a single
    // flattened cone (the upstream comment on the realistic demo
    // describes exactly this trade-off). The total burst runs ~2.5s
    // and then stops, so a user who lingers on the dashboard
    // doesn't get an ongoing animation every time they refocus
    // the tab.
    //
    // disableForReducedMotion: true is the library's built-in
    // escape hatch — when the user has the OS-level reduced-motion
    // preference set, the entire burst no-ops.
    function fireFullTeamBurst() {
        var count = 200;
        var defaults = { origin: { y: 0.7 }, disableForReducedMotion: true };

        function fire(particleRatio, opts) {
            window.confetti(Object.assign({}, defaults, opts, {
                particleCount: Math.floor(count * particleRatio)
            }));
        }

        fire(0.25, { spread: 26, startVelocity: 55 });
        fire(0.20, { spread: 60 });
        fire(0.35, { spread: 100, decay: 0.91, scalar: 0.8 });
        fire(0.10, { spread: 120, startVelocity: 25, decay: 0.92, scalar: 1.2 });
        fire(0.10, { spread: 120, startVelocity: 45 });

        // Two final side-cannons a moment after the main burst,
        // timed to feel like an "encore" rather than part of the
        // same continuous stream. 250ms matches the upstream
        // fireworks demo interval.
        setTimeout(function () {
            window.confetti(Object.assign({}, defaults, {
                particleCount: 80,
                angle: 60,
                spread: 55,
                origin: { x: 0, y: 0.65 }
            }));
            window.confetti(Object.assign({}, defaults, {
                particleCount: 80,
                angle: 120,
                spread: 55,
                origin: { x: 1, y: 0.65 }
            }));
        }, 250);
    }

    // startDecemberSnow fires the upstream snow() recipe — a single
    // particle per animation frame with low gravity, low velocity,
    // and a small horizontal drift. White circles only, scaled
    // randomly so flakes vary in apparent size. The loop runs for
    // ~45 seconds and then stops; long enough to feel like a
    // winter scene when the user opens the dashboard, short
    // enough not to drain CPU on a tab the user backgrounds.
    //
    // The upstream demo runs 15 seconds; we extended to 45s because
    // "make it snow in December" implies more than a brief flurry.
    // The skew variable mirrors the upstream demo: it slowly
    // tightens so flakes progressively appear higher in the
    // viewport as the storm matures.
    function startDecemberSnow() {
        var duration = 45 * 1000;
        var animationEnd = Date.now() + duration;
        var skew = 1;

        function randomInRange(min, max) {
            return Math.random() * (max - min) + min;
        }

        (function frame() {
            var timeLeft = animationEnd - Date.now();
            var ticks = Math.max(200, 500 * (timeLeft / duration));
            skew = Math.max(0.8, skew - 0.001);

            window.confetti({
                particleCount: 1,
                startVelocity: 0,
                ticks: ticks,
                origin: {
                    x: Math.random(),
                    // since particles fall down, skew start toward the top
                    y: (Math.random() * skew) - 0.2
                },
                colors: ['#ffffff'],
                shapes: ['circle'],
                gravity: randomInRange(0.4, 0.6),
                scalar: randomInRange(0.4, 1),
                drift: randomInRange(-0.4, 0.4)
            });

            if (timeLeft > 0) {
                requestAnimationFrame(frame);
            }
        }());
    }

    // prefersReducedMotion reports whether the OS-level
    // reduced-motion preference is set. Used by startDecemberSnow
    // because canvas-confetti's disableForReducedMotion option
    // only covers individual confetti() calls, not the
    // requestAnimationFrame loop driving the snow storm.
    function prefersReducedMotion() {
        if (typeof window.matchMedia !== 'function') {
            return false;
        }
        return window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();
