package web

import (
	"testing"

	"github.com/inful/madhatter/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewHandler_ConfettiEnabledDefaultsTrue pins the default:
// CONFETTI_ENABLED unset (the production startup case) leaves the
// flag true so the dashboard's full-team-on-site celebration
// fires as the issue intends. envutil.Bool falls back to the
// default when the env var is missing or unparseable.
func TestNewHandler_ConfettiEnabledDefaultsTrue(t *testing.T) {
	t.Setenv("CONFETTI_ENABLED", "")

	h, err := NewHandler(nil, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)
	assert.True(t, h.confettiEnabled, "CONFETTI_ENABLED unset must default to true")
}

// TestNewHandler_ConfettiEnabledFalseExplicit pins the
// CONFETTI_ENABLED=false opt-out: the operator-level escape hatch
// from the documentation. Setting it to false must reach the
// handler's field so loadDashboardData's funEffectsFor call
// short-circuits before checking the date context.
func TestNewHandler_ConfettiEnabledFalseExplicit(t *testing.T) {
	t.Setenv("CONFETTI_ENABLED", "false")

	h, err := NewHandler(nil, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)
	assert.False(t, h.confettiEnabled, "CONFETTI_ENABLED=false must reach h.confettiEnabled")
}

// TestNewHandler_SnowEnabledDefaultsTrue pins the snow default:
// SNOW_ENABLED unset leaves the flag true so December triggers
// the snow storm as the issue intends.
func TestNewHandler_SnowEnabledDefaultsTrue(t *testing.T) {
	t.Setenv("SNOW_ENABLED", "")

	h, err := NewHandler(nil, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)
	assert.True(t, h.snowEnabled, "SNOW_ENABLED unset must default to true")
}

// TestNewHandler_SnowEnabledFalseExplicit pins the
// SNOW_ENABLED=false opt-out: snow affects every December day, so
// an operator who finds it distracting must have a single flag
// to disable it without touching the confetti path.
func TestNewHandler_SnowEnabledFalseExplicit(t *testing.T) {
	t.Setenv("SNOW_ENABLED", "false")

	h, err := NewHandler(nil, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)
	assert.False(t, h.snowEnabled, "SNOW_ENABLED=false must reach h.snowEnabled")
}

// TestNewHandler_LeavesEnabledDefaultsTrue pins the leaves
// default: LEAVES_ENABLED unset leaves the flag true so
// September 23 with the full team on-site triggers the falling-
// leaves storm. The effect piggybacks on the same full-team-on-
// site + business-day gate as confetti, but its calendar axis
// (autumnal equinox, day 23 of September) is what distinguishes
// it from the always-on confetti.
func TestNewHandler_LeavesEnabledDefaultsTrue(t *testing.T) {
	t.Setenv("LEAVES_ENABLED", "")

	h, err := NewHandler(nil, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)
	assert.True(t, h.leavesEnabled, "LEAVES_ENABLED unset must default to true")
}

// TestNewHandler_LeavesEnabledFalseExplicit pins the
// LEAVES_ENABLED=false opt-out: the equinox-day falling-leaves
// storm is the most narrowly-scoped of the three effects (one
// day a year) so the operator-level disable matters even more
// than for the always-running snow storm — one flag flips the
// equinox celebration off without touching the other two effects.
func TestNewHandler_LeavesEnabledFalseExplicit(t *testing.T) {
	t.Setenv("LEAVES_ENABLED", "false")

	h, err := NewHandler(nil, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)
	assert.False(t, h.leavesEnabled, "LEAVES_ENABLED=false must reach h.leavesEnabled")
}

// TestNewHandler_FunEffectsGatesIndependent pins that the three
// flags are wired independently: setting CONFETTI_ENABLED=false
// must not affect h.snowEnabled or h.leavesEnabled, and vice
// versa. The dashboard surfaces the three effects as orthogonal
// features, so a config typo on one axis must not silently flip
// either of the others.
func TestNewHandler_FunEffectsGatesIndependent(t *testing.T) {
	t.Run("only confetti on", func(t *testing.T) {
		t.Setenv("CONFETTI_ENABLED", "")
		t.Setenv("SNOW_ENABLED", "false")
		t.Setenv("LEAVES_ENABLED", "false")

		h, err := NewHandler(nil, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
		require.NoError(t, err)
		assert.True(t, h.confettiEnabled)
		assert.False(t, h.snowEnabled)
		assert.False(t, h.leavesEnabled)
	})

	t.Run("only snow on", func(t *testing.T) {
		t.Setenv("CONFETTI_ENABLED", "false")
		t.Setenv("SNOW_ENABLED", "")
		t.Setenv("LEAVES_ENABLED", "false")

		h, err := NewHandler(nil, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
		require.NoError(t, err)
		assert.False(t, h.confettiEnabled)
		assert.True(t, h.snowEnabled)
		assert.False(t, h.leavesEnabled)
	})

	t.Run("only leaves on", func(t *testing.T) {
		t.Setenv("CONFETTI_ENABLED", "false")
		t.Setenv("SNOW_ENABLED", "false")
		t.Setenv("LEAVES_ENABLED", "")

		h, err := NewHandler(nil, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
		require.NoError(t, err)
		assert.False(t, h.confettiEnabled)
		assert.False(t, h.snowEnabled)
		assert.True(t, h.leavesEnabled)
	})

	t.Run("all on", func(t *testing.T) {
		t.Setenv("CONFETTI_ENABLED", "")
		t.Setenv("SNOW_ENABLED", "")
		t.Setenv("LEAVES_ENABLED", "")

		h, err := NewHandler(nil, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
		require.NoError(t, err)
		assert.True(t, h.confettiEnabled)
		assert.True(t, h.snowEnabled)
		assert.True(t, h.leavesEnabled)
	})

	t.Run("all off", func(t *testing.T) {
		t.Setenv("CONFETTI_ENABLED", "false")
		t.Setenv("SNOW_ENABLED", "false")
		t.Setenv("LEAVES_ENABLED", "false")

		h, err := NewHandler(nil, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
		require.NoError(t, err)
		assert.False(t, h.confettiEnabled)
		assert.False(t, h.snowEnabled)
		assert.False(t, h.leavesEnabled)
	})
}
