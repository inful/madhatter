package web

import (
	"testing"

	"github.com/inful/madhatter/internal/version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestVersionFuncMapRenders verifies that the {{version}} FuncMap
// function resolves to a non-empty string. The package-level vars
// may be overridden by ldflags at build time, but the FuncMap must
// always return at least the fallback "dev" so dev/test renders
// don't crash.
func TestVersionFuncMapRenders(t *testing.T) {
	// Reset package state for the test so the FuncMap reflects what
	// the actual binary would print. Tests run with no ldflags so
	// the fallback applies.
	restoreVersion := version.Version
	restoreCommit := version.Commit
	t.Cleanup(func() {
		version.Version = restoreVersion
		version.Commit = restoreCommit
	})

	tmpl, err := parseTemplates()
	require.NoError(t, err)
	require.NotNil(t, tmpl)

	t.Run("default fallback renders dev", func(t *testing.T) {
		version.Version = ""
		version.Commit = ""

		var out string
		require.NoError(t, tmpl.ExecuteTemplate(&stringWriter{&out}, "version_footer_test", nil))
		// The template should at minimum contain a non-empty
		// version string. With Version="" the fallback fires
		// and we get "dev".
		assert.NotEmpty(t, out)
	})

	t.Run("version-only render", func(t *testing.T) {
		version.Version = "v0.32.3"
		version.Commit = ""

		var out string
		require.NoError(t, tmpl.ExecuteTemplate(&stringWriter{&out}, "version_footer_test", nil))
		assert.Contains(t, out, "v0.32.3")
	})

	t.Run("version+commit render", func(t *testing.T) {
		version.Version = "v0.32.3"
		version.Commit = "abc1234"

		var out string
		require.NoError(t, tmpl.ExecuteTemplate(&stringWriter{&out}, "version_footer_test", nil))
		// html/template HTML-escapes the '+' to '&#43;' in text
		// context. The visually-rendered output is still 'v0.32.3
		// +abc1234' for the user; assert the parts separately so
		// the assertion is stable across html/template escape
		// rules.
		assert.Contains(t, out, "v0.32.3")
		assert.Contains(t, out, "abc1234")
	})
}

// TestVersionCurrentFallback pins the Current() helper's fallback
// semantics so a future refactor can't silently emit an empty
// string in dev builds.
func TestVersionCurrentFallback(t *testing.T) {
	restoreVersion := version.Version
	restoreCommit := version.Commit
	t.Cleanup(func() {
		version.Version = restoreVersion
		version.Commit = restoreCommit
	})

	t.Run("empty version falls back to dev", func(t *testing.T) {
		version.Version = ""
		version.Commit = ""
		assert.Equal(t, "dev", version.Current())
	})

	t.Run("version without commit", func(t *testing.T) {
		version.Version = "v1.2.3"
		version.Commit = ""
		assert.Equal(t, "v1.2.3", version.Current())
	})

	t.Run("version with commit", func(t *testing.T) {
		version.Version = "v1.2.3"
		version.Commit = "deadbeef"
		assert.Equal(t, "v1.2.3+deadbeef", version.Current())
	})
}

// stringWriter is a minimal io.Writer that writes into a *string
// pointer. Used to capture template output without pulling in
// bytes.Buffer for a one-line capture.
type stringWriter struct {
	out *string
}

func (w *stringWriter) Write(p []byte) (int, error) {
	*w.out += string(p)
	return len(p), nil
}
