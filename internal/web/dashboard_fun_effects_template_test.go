package web

import (
	"net/http/httptest"
	"testing"

	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// renderDashboardForFunEffects executes the dashboard template
// with a minimal data map that includes only the fields the
// fun-effects config div reads. The other dashboard sections
// gracefully degrade to "no data" when their inputs are missing,
// which is exactly what we want for an isolated test of the
// fun-effects plumbing.
func renderDashboardForFunEffects(t *testing.T, funEffectsConfetti, funEffectsSnow, funEffectsLeaves bool) string {
	t.Helper()

	mockDB := &database.DB{}
	handler, err := NewHandler(mockDB, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	data := map[string]any{
		"User":               map[string]any{"Email": "alice@example.com", "Name": "Alice"},
		"IsAdmin":            false,
		"Template":           "dashboard",
		"FunEffectsConfetti": funEffectsConfetti,
		"FunEffectsSnow":     funEffectsSnow,
		"FunEffectsLeaves":   funEffectsLeaves,
	}

	w := httptest.NewRecorder()
	require.NoError(t, handler.tmpl.ExecuteTemplate(w, "dashboard.html", data))
	return w.Body.String()
}

// TestDashboard_FunEffects_ConfigDivAndScripts pins that when
// FunEffectsConfetti or FunEffectsSnow is true, the dashboard HTML
// contains:
//
//   - the canvas-confetti.browser.min.js script tag,
//   - the fun-effects.js script tag,
//   - the #fun-effects-config div with the matching data-confetti
//     and data-snow attributes.
//
// This is the contract fun-effects.js reads at runtime; if any
// of these go missing, the effect silently no-ops in the browser.
func TestDashboard_FunEffects_ConfigDivAndScripts(t *testing.T) {
	body := renderDashboardForFunEffects(t, true, true, true)

	assert.Contains(t, body, `src="/static/js/canvas-confetti.browser.min.js"`,
		"the vendored canvas-confetti bundle must be loaded on the dashboard when fun effects are enabled")
	assert.Contains(t, body, `src="/static/js/fun-effects.js"`,
		"the fun-effects wrapper must be loaded on the dashboard when fun effects are enabled")
	assert.Contains(t, body, `id="fun-effects-config"`,
		"the hidden config div must be present so fun-effects.js can read the flags")
	assert.Contains(t, body, `data-confetti="true"`,
		"data-confetti must be true when FunEffectsConfetti=true")
	assert.Contains(t, body, `data-snow="true"`,
		"data-snow must be true when FunEffectsSnow=true")
	assert.Contains(t, body, `data-leaves="true"`,
		"data-leaves must be true when FunEffectsLeaves=true")
}

// TestDashboard_FunEffects_FalseFlagsRenderFalse pins the negative
// case: when all flags are false, the config div carries false
// and the scripts are still loaded. The script tags ship
// unconditionally because the disabled-effect path is data-driven,
// not script-loading-driven — saves a round-trip and keeps the
// CSP simple.
func TestDashboard_FunEffects_FalseFlagsRenderFalse(t *testing.T) {
	body := renderDashboardForFunEffects(t, false, false, false)

	assert.Contains(t, body, `data-confetti="false"`,
		"data-confetti must be false when FunEffectsConfetti=false")
	assert.Contains(t, body, `data-snow="false"`,
		"data-snow must be false when FunEffectsSnow=false")
	assert.Contains(t, body, `data-leaves="false"`,
		"data-leaves must be false when FunEffectsLeaves=false")
	// The script tags still load even when all flags are false
	// — fun-effects.js no-ops on data-X="false". This avoids
	// the page re-fetching the vendored bundle on the rare day
	// the gate flips.
	assert.Contains(t, body, `src="/static/js/canvas-confetti.browser.min.js"`,
		"the vendored canvas-confetti bundle is loaded unconditionally; fun-effects.js no-ops on disabled flags")
	assert.Contains(t, body, `src="/static/js/fun-effects.js"`,
		"the fun-effects wrapper is loaded unconditionally; fun-effects.js no-ops on disabled flags")
}

// TestDashboard_FunEffects_OnlyConfettiTrue pins the partial case:
// confetti fires (today is a full-team business day) but it is
// not December and not the equinox, so only data-confetti is
// true. The other attributes stay false.
func TestDashboard_FunEffects_OnlyConfettiTrue(t *testing.T) {
	body := renderDashboardForFunEffects(t, true, false, false)

	assert.Contains(t, body, `data-confetti="true"`)
	assert.Contains(t, body, `data-snow="false"`)
	assert.Contains(t, body, `data-leaves="false"`)
}

// TestDashboard_FunEffects_OnlySnowTrue pins the December case:
// it is December and today is not a full-team day, so only
// data-snow is true. fun-effects.js will run the snow storm but
// skip the confetti burst.
func TestDashboard_FunEffects_OnlySnowTrue(t *testing.T) {
	body := renderDashboardForFunEffects(t, false, true, false)

	assert.Contains(t, body, `data-confetti="false"`)
	assert.Contains(t, body, `data-snow="true"`)
	assert.Contains(t, body, `data-leaves="false"`)
}

// TestDashboard_FunEffects_OnlyLeavesTrue pins the equinox case:
// today is Sept 23 with the full team on-site, but confetti's
// gates aren't all true (the test renders only one flag at a
// time). The leaves attribute is the one under test.
func TestDashboard_FunEffects_OnlyLeavesTrue(t *testing.T) {
	body := renderDashboardForFunEffects(t, false, false, true)

	assert.Contains(t, body, `data-confetti="false"`)
	assert.Contains(t, body, `data-snow="false"`)
	assert.Contains(t, body, `data-leaves="true"`)
}

// TestDashboard_FunEffects_HiddenDivIsAriaHidden pins the
// accessibility guard: the config div is screen-reader-skipped
// (aria-hidden=true) because it carries no readable text — only
// data attributes consumed by JS. A screen reader should never
// announce it.
func TestDashboard_FunEffects_HiddenDivIsAriaHidden(t *testing.T) {
	body := renderDashboardForFunEffects(t, true, true, true)

	assert.Contains(t, body, `aria-hidden="true"`,
		"the fun-effects config div must be hidden from screen readers")
	assert.Contains(t, body, `style="display:none"`,
		"the fun-effects config div must be visually hidden")
}
