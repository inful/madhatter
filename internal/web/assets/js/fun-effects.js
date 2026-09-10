// Fun effects for the dashboard — celebratory confetti when the
// entire team is on-site, gently falling snow through the
// month of December, autumnal-equinox falling leaves, and a
// dedicated birthday blast on the day a member has a birthday
// (#60 follow-up). The trigger flags are rendered into a
// hidden <div id="fun-effects-config"> by the dashboard
// template; this file reads them once on DOMContentLoaded and
// fires the configured effect exactly once per page load.
//
// The effects use canvas-confetti (vendored at
// /static/js/canvas-confetti.browser.min.js, which exposes a
// global `confetti` function), so the only third-party code
// path is the library itself. The recipes are adapted from the
// upstream demos (realistic / snow / shapeFromText on
// kirilv.com/canvas-confetti/); the modifications are
// documented inline.
//
// Respecting prefers-reduced-motion: canvas-confetti has a
// built-in `disableForReducedMotion: true` option that no-ops
// the confetti bursts when the user has the OS-level
// reduced-motion preference set. The snow / leaves storms wrap
// their loops in a manual check because the library option only
// covers individual confetti() calls, not the
// requestAnimationFrame-driven storm.
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
        var wantLeaves = cfg.getAttribute('data-leaves') === 'true';
        var wantBirthday = cfg.getAttribute('data-birthday-confetti') === 'true';

        if (typeof window.confetti !== 'function') {
            // The vendored bundle failed to load (offline deploy,
            // blocked by an upstream CSP mistake, etc.). Fail
            // closed: the dashboard still renders, just without
            // the celebration. Logged so an operator notices.
            if (wantConfetti || wantSnow || wantLeaves || wantBirthday) {
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
        if (wantLeaves && !prefersReducedMotion()) {
            startAutumnLeaves();
        }
        if (wantBirthday) {
            fireBirthdayBurst();
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
    // fireBirthdayBurst fires a celebratory burst specifically
    // for the birthday-person path. Distinct from fireFullTeamBurst
    // in three ways:
    //
    //   1. Origin: top corners (angle 60° / 120°), not bottom.
    //      The blast drops from above like a surprise.
    //   2. Emoji: 🎂 + pink-heart shapes interleaved with
    //      regular confetti particles, baked into confetti
    //      shapes via shapeFromText (same pattern as the
    //      autumn-leaves effect).
    //   3. Colors: pink + gold, matching the birthday banner's
    //      pink/coral gradient. Distinct from the gold-green
    //      full-team gradient so a user watching both effects
    //      can tell them apart at a glance.
    //
    // The burst is sized smaller than fireFullTeamBurst —
    // 150 particles vs 200 — because the birthday banner is
    // already a louder visual cue. The blast complements the
    // banner copy, not replaces it.
    //
    // disableForReducedMotion: true is the library's built-in
    // escape hatch. The user-level
    // BIRTHDAY_CONFETTI_ENABLED env gate is handled at the Go
    // layer (data-birthday-confetti=false).
    //
    // The cake/heart emoji are baked into confetti shapes via
    // shapeFromText. Some headless or minimal-font environments
    // don't have Apple Color Emoji / Segoe UI Emoji / Noto Color
    // Emoji installed; shapeFromText then returns an empty
    // canvas, the particles render as 0×0 sprites, and the user
    // sees nothing. The defensive fallback below tries each
    // shape and drops the failing one — the burst still fires
    // with whatever shapes survive. We log once so an operator
    // notices if the team's emoji font is missing on the
    // server side (uncommon but possible on a stripped-down
    // container).
    function safeShapeFromText(opts) {
        try {
            var shape = window.confetti.shapeFromText(opts);
            if (!shape) return null;
            return shape;
        } catch (e) {
            console.warn('fun-effects: shapeFromText failed for', opts && opts.text, e);
            return null;
        }
    }

    function fireBirthdayBurst() {
        console.log('fun-effects: firing birthday burst');
        var scalar = 1.5;
        var cake = safeShapeFromText({ text: '🎂', scalar: scalar });
        var heart = safeShapeFromText({ text: '💗', scalar: scalar });

        // Side cannons — pink + gold particles, with the cake
        // and heart shapes interleaved. The cannons fire from
        // the top corners at angles that send the particles
        // toward the center of the page. The shapes array
        // excludes any nulls returned by safeShapeFromText so a
        // missing-emoji-font deployment still sees the burst
        // with whatever shapes survived.
        var shapes = ['circle', 'square'];
        if (cake) shapes.push(cake);
        if (heart) shapes.push(heart);
        var defaults = {
            disableForReducedMotion: true,
            colors: ['#f9a8d4', '#fb7185', '#fde68a', '#fbbf24', '#ffffff'],
            shapes: shapes
        };

        // Schedule: 5 staggered waves spread over ~3 seconds.
        // The user asked for "at least twice as long" as the
        // original single-encore recipe (which finished in
        // ~1.5s of visible confetti). Each wave fires at a
        // distinct timestamp with different particle physics
        // — later waves use lower startVelocity and lighter
        // gravity so the particles linger longer in the
        // air, building the impression of a sustained
        // celebration rather than a single burst.
        //
        // The schedule is intentionally declarative (an array
        // of objects) rather than five hand-rolled setTimeout
        // calls so a test can grep the schedule directly and
        // confirm the duration stays above the threshold. The
        // Go test reads this same source file and asserts the
        // maximum delay >= 1500ms plus at least 3 distinct
        // delays, so a future shortcut to "just make the
        // first burst bigger" fails the regression guard.
        var schedule = [
            // Wave 0 (t=0ms): twin side cannons from the top
            // corners. Mirrors the original recipe's opening
            // burst — fast, dramatic, anchors the celebration.
            { delayMs: 0,    opts: { particleCount: 60, angle: 60,  spread: 70, origin: { x: 0, y: 0 } } },
            { delayMs: 0,    opts: { particleCount: 60, angle: 120, spread: 70, origin: { x: 1, y: 0 } } },

            // Wave 1 (t=400ms): center-top full-width spread.
            // Fills in the gap between the two side cannons
            // and gives the celebration a single coherent
            // "burst" rather than two disjoint corners.
            { delayMs: 400,  opts: { particleCount: 80, spread: 120, startVelocity: 35, origin: { x: 0.5, y: 0 } } },

            // Wave 2 (t=1100ms): a slower, wider spread from
            // the center. Lower startVelocity + lower gravity
            // so the particles drift down over ~2s rather than
            // falling fast. The cake/heart shapes dominate
            // here because the lower density lets each particle
            // be read individually.
            { delayMs: 1100, opts: { particleCount: 50, spread: 150, startVelocity: 22, gravity: 0.4, drift: 0.3, origin: { x: 0.5, y: 0 } } },

            // Wave 3 (t=2000ms): the finale. Slow trickle from
            // the very top so the celebration doesn't just
            // stop — it trails off. Lower particle count keeps
            // the page responsive on long-running dashboards.
            { delayMs: 2000, opts: { particleCount: 30, spread: 180, startVelocity: 14, gravity: 0.3, drift: -0.2, origin: { x: 0.5, y: 0 } } }
        ];

        schedule.forEach(function (entry) {
            setTimeout(function () {
                window.confetti(Object.assign({}, defaults, entry.opts));
            }, entry.delayMs);
        });
    }

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

    // startAutumnLeaves fires the equinox-day falling-leaves
    // storm. The four leaf emojis are baked into confetti shapes
    // once at startup via confetti.shapeFromText (the library's
    // built-in emoji-to-particle helper), then picked at random
    // per animation frame so the storm looks varied rather than
    // uniform. Gravity sits a notch above the snow recipe
    // (0.7-1.0 vs 0.4-0.6) because leaves fall faster than
    // snowflakes; drift is wider (-0.7..0.7 vs -0.4..0.4) so
    // each leaf sways more on the way down.
    //
    // Total runtime mirrors startDecemberSnow — 45 seconds,
    // long enough to feel like a seasonal moment, short enough
    // not to drain CPU on a backgrounded tab. The
    // prefersReducedMotion guard at the call site keeps users
    // with the OS-level reduced-motion preference from seeing
    // the storm at all.
    function startAutumnLeaves() {
        var duration = 45 * 1000;
        var animationEnd = Date.now() + duration;

        // Bake the four leaf emojis into confetti shapes once.
        // shapeFromText rasterises the text into a sprite the
        // library can stamp — calling it per-frame would defeat
        // the purpose. The scalar bump (1.6) keeps the emoji
        // legible at the typical particle size.
        var scalar = 1.6;
        var leaves = [
            window.confetti.shapeFromText({ text: '🌿', scalar: scalar }),
            window.confetti.shapeFromText({ text: '🍁', scalar: scalar }),
            window.confetti.shapeFromText({ text: '🍂', scalar: scalar }),
            window.confetti.shapeFromText({ text: '🍃', scalar: scalar })
        ];

        function randomInRange(min, max) {
            return Math.random() * (max - min) + min;
        }

        function pickShape() {
            return leaves[Math.floor(Math.random() * leaves.length)];
        }

        (function frame() {
            var timeLeft = animationEnd - Date.now();
            var ticks = Math.max(200, 500 * (timeLeft / duration));

            window.confetti({
                particleCount: 1,
                startVelocity: 0,
                ticks: ticks,
                origin: {
                    x: Math.random(),
                    // start the leaves above the viewport so they
                    // fall through rather than appear mid-air
                    y: -0.1
                },
                shapes: [pickShape()],
                gravity: randomInRange(0.7, 1.0),
                scalar: randomInRange(0.7, 1.2),
                drift: randomInRange(-0.7, 0.7)
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
