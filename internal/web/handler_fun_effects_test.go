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

// TestNewHandler_FunEffectsGatesIndependent pins that the two
// flags are wired independently: setting CONFETTI_ENABLED=false
// must not affect h.snowEnabled, and vice versa. The dashboard
// surfaces the two effects as orthogonal features, so a config
// typo on one axis must not silently flip the other.
func TestNewHandler_FunEffectsGatesIndependent(t *testing.T) {
	t.Run("confetti off, snow on", func(t *testing.T) {
		t.Setenv("CONFETTI_ENABLED", "false")
		t.Setenv("SNOW_ENABLED", "")

		h, err := NewHandler(nil, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
		require.NoError(t, err)
		assert.False(t, h.confettiEnabled)
		assert.True(t, h.snowEnabled)
	})

	t.Run("confetti on, snow off", func(t *testing.T) {
		t.Setenv("CONFETTI_ENABLED", "")
		t.Setenv("SNOW_ENABLED", "false")

		h, err := NewHandler(nil, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
		require.NoError(t, err)
		assert.True(t, h.confettiEnabled)
		assert.False(t, h.snowEnabled)
	})

	t.Run("both off", func(t *testing.T) {
		t.Setenv("CONFETTI_ENABLED", "false")
		t.Setenv("SNOW_ENABLED", "false")

		h, err := NewHandler(nil, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
		require.NoError(t, err)
		assert.False(t, h.confettiEnabled)
		assert.False(t, h.snowEnabled)
	})
}
