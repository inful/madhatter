package envutil

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBool locks in the contract of Bool: empty/unset/garbage →
// default; otherwise parsed via strconv.ParseBool.
func TestBool(t *testing.T) {
	t.Run("UnsetReturnsDefault", func(t *testing.T) {
		key := "ENVUTIL_TEST_UNSET_BOOL"
		require.NoError(t, os.Unsetenv(key))
		assert.True(t, Bool(key, true))
		assert.False(t, Bool(key, false))
	})

	t.Run("EmptyReturnsDefault", func(t *testing.T) {
		key := "ENVUTIL_TEST_EMPTY_BOOL"
		t.Setenv(key, "")
		assert.True(t, Bool(key, true))
		assert.False(t, Bool(key, false))
	})

	t.Run("ParsesTruthyValues", func(t *testing.T) {
		key := "ENVUTIL_TEST_TRUTHY_BOOL"
		for _, v := range []string{"true", "TRUE", "True", "1", "t", "T"} {
			t.Setenv(key, v)
			assert.True(t, Bool(key, false), "value %q must parse true", v)
		}
	})

	t.Run("ParsesFalsyValues", func(t *testing.T) {
		key := "ENVUTIL_TEST_FALSY_BOOL"
		for _, v := range []string{"false", "FALSE", "False", "0", "f", "F"} {
			t.Setenv(key, v)
			assert.False(t, Bool(key, true), "value %q must parse false", v)
		}
	})

	t.Run("GarbageFallsBackToDefault", func(t *testing.T) {
		key := "ENVUTIL_TEST_GARBAGE_BOOL"
		t.Setenv(key, "not-a-bool")
		assert.True(t, Bool(key, true), "garbage must fall back to default true")
		assert.False(t, Bool(key, false), "garbage must fall back to default false")
	})
}

// TestInt covers the int helper.
func TestInt(t *testing.T) {
	t.Run("UnsetReturnsDefault", func(t *testing.T) {
		key := "ENVUTIL_TEST_UNSET_INT"
		require.NoError(t, os.Unsetenv(key))
		assert.Equal(t, 42, Int(key, 42))
	})

	t.Run("ParsesValue", func(t *testing.T) {
		key := "ENVUTIL_TEST_PARSE_INT"
		t.Setenv(key, "7")
		assert.Equal(t, 7, Int(key, 0))
	})

	t.Run("GarbageFallsBackToDefault", func(t *testing.T) {
		key := "ENVUTIL_TEST_GARBAGE_INT"
		t.Setenv(key, "not-an-int")
		assert.Equal(t, 99, Int(key, 99))
	})

	t.Run("Negative", func(t *testing.T) {
		key := "ENVUTIL_TEST_NEGATIVE_INT"
		t.Setenv(key, "-5")
		assert.Equal(t, -5, Int(key, 0))
	})
}

// TestFloat64 covers the float helper.
func TestFloat64(t *testing.T) {
	t.Run("UnsetReturnsDefault", func(t *testing.T) {
		key := "ENVUTIL_TEST_UNSET_FLOAT"
		require.NoError(t, os.Unsetenv(key))
		assert.InDelta(t, 3.14, Float64(key, 3.14), 0.0001)
	})

	t.Run("ParsesValue", func(t *testing.T) {
		key := "ENVUTIL_TEST_PARSE_FLOAT"
		t.Setenv(key, "2.5")
		assert.InDelta(t, 2.5, Float64(key, 0), 0.0001)
	})

	t.Run("ParsesIntegerAsFloat", func(t *testing.T) {
		key := "ENVUTIL_TEST_INT_AS_FLOAT"
		t.Setenv(key, "7")
		assert.InDelta(t, 7.0, Float64(key, 0), 0.0001)
	})

	t.Run("GarbageFallsBackToDefault", func(t *testing.T) {
		key := "ENVUTIL_TEST_GARBAGE_FLOAT"
		t.Setenv(key, "nope")
		assert.InDelta(t, 1.5, Float64(key, 1.5), 0.0001)
	})
}

// TestString covers the string helper.
func TestString(t *testing.T) {
	t.Run("UnsetReturnsDefault", func(t *testing.T) {
		key := "ENVUTIL_TEST_UNSET_STR"
		require.NoError(t, os.Unsetenv(key))
		assert.Equal(t, "fallback", String(key, "fallback"))
	})

	t.Run("EmptyReturnsDefault", func(t *testing.T) {
		key := "ENVUTIL_TEST_EMPTY_STR"
		t.Setenv(key, "")
		assert.Equal(t, "fallback", String(key, "fallback"))
	})

	t.Run("ParsesValue", func(t *testing.T) {
		key := "ENVUTIL_TEST_PARSE_STR"
		t.Setenv(key, "value")
		assert.Equal(t, "value", String(key, "fallback"))
	})

	t.Run("WhitespacePreserved", func(t *testing.T) {
		// String does NOT trim; callers who want trimmed values
		// must call strings.TrimSpace themselves. Lock that in.
		key := "ENVUTIL_TEST_WHITESPACE_STR"
		t.Setenv(key, "  spaces  ")
		assert.Equal(t, "  spaces  ", String(key, "fallback"))
	})
}

// TestDuration covers the duration helper.
func TestDuration(t *testing.T) {
	t.Run("UnsetReturnsDefault", func(t *testing.T) {
		key := "ENVUTIL_TEST_UNSET_DUR"
		require.NoError(t, os.Unsetenv(key))
		assert.Equal(t, 30*time.Second, Duration(key, 30*time.Second))
	})

	t.Run("ParsesValue", func(t *testing.T) {
		key := "ENVUTIL_TEST_PARSE_DUR"
		t.Setenv(key, "5m")
		assert.Equal(t, 5*time.Minute, Duration(key, time.Second))
	})

	t.Run("ParsesCompoundValue", func(t *testing.T) {
		key := "ENVUTIL_TEST_COMPOUND_DUR"
		t.Setenv(key, "1h30m")
		assert.Equal(t, 90*time.Minute, Duration(key, time.Second))
	})

	t.Run("GarbageFallsBackToDefault", func(t *testing.T) {
		key := "ENVUTIL_TEST_GARBAGE_DUR"
		t.Setenv(key, "nope")
		assert.Equal(t, 7*time.Second, Duration(key, 7*time.Second))
	})
}
