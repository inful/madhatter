package api

import (
	"path/filepath"
	"testing"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newAuthSetupDB returns a fresh, file-backed test database. The
// auth-setup helpers exercise migrations and full DB lifecycle, so a
// temp directory (not :memory:) gives them a real file to back up
// against.
func newAuthSetupDB(t *testing.T) *database.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "auth-setup.db")
	db, err := database.New(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestSetupAuth_DevelopmentShortCircuit verifies that the wrapper
// dispatches to setupDevelopmentAuth when development=true.
func TestSetupAuth_DevelopmentShortCircuit(t *testing.T) {
	t.Setenv("TOKEN_ENCRYPTION_KEY", "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI=")
	db := newAuthSetupDB(t)

	authManager, middleware, sessionManager, err := setupAuth(db, true)
	require.NoError(t, err)
	require.NotNil(t, authManager)
	require.NotNil(t, middleware)
	require.NotNil(t, sessionManager)

	// The fake provider is registered by setupDevelopmentAuth.
	_, err = authManager.GetProvider("fake")
	assert.NoError(t, err, "fake provider must be registered in dev mode")
}

// TestSetupAuth_ProductionShortCircuit verifies that the wrapper
// dispatches to setupProductionAuth when development=false.
//
// In a fresh test environment no OAuth env vars are set, so
// setupProductionAuth returns a tuple of four nils and logs a
// "WARNING: Authentication disabled" message. That all-nil return
// is the documented contract — auth is wired but no-op.
func TestSetupAuth_ProductionShortCircuit(t *testing.T) {
	t.Setenv("TOKEN_ENCRYPTION_KEY", "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI=")
	// Make sure no provider env vars leak in from the host environment.
	for _, k := range []string{"FORGEJO_CLIENT_ID", "GITLAB_CLIENT_ID"} {
		t.Setenv(k, "")
	}

	db := newAuthSetupDB(t)
	authManager, middleware, sessionManager, err := setupAuth(db, false)
	require.NoError(t, err)
	assert.Nil(t, authManager, "auth disabled when no providers configured")
	assert.Nil(t, middleware)
	assert.Nil(t, sessionManager)
}

// TestSetupProductionAuth_NoEnvVars pins down the "no OAuth
// configured" path: the function must return a clean all-nil tuple
// so the server can boot without auth.
func TestSetupProductionAuth_NoEnvVars(t *testing.T) {
	t.Setenv("TOKEN_ENCRYPTION_KEY", "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI=")
	for _, k := range []string{"FORGEJO_CLIENT_ID", "GITLAB_CLIENT_ID"} {
		t.Setenv(k, "")
	}

	db := newAuthSetupDB(t)
	authManager, middleware, sessionManager, err := setupProductionAuth(db)
	require.NoError(t, err)
	assert.Nil(t, authManager)
	assert.Nil(t, middleware)
	assert.Nil(t, sessionManager)
}

// TestSetupProductionAuth_WithGitLabConfig verifies the happy path:
// a complete GITLAB_CLIENT_ID + secret + redirect configuration wires
// the auth components. We don't make any HTTP calls here — the
// GitLab provider has its own test coverage in internal/auth. This
// test only asserts that setupProductionAuth doesn't fail and that
// the provider registry has the expected entry.
func TestSetupProductionAuth_WithGitLabConfig(t *testing.T) {
	t.Setenv("TOKEN_ENCRYPTION_KEY", "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI=")
	t.Setenv("GITLAB_CLIENT_ID", "test-client-id")
	t.Setenv("GITLAB_CLIENT_SECRET", "test-client-secret")
	t.Setenv("GITLAB_REDIRECT_URL", "http://localhost:8080/auth/callback?provider=gitlab")

	db := newAuthSetupDB(t)
	authManager, middleware, sessionManager, err := setupProductionAuth(db)
	require.NoError(t, err)
	require.NotNil(t, authManager)
	require.NotNil(t, middleware)
	require.NotNil(t, sessionManager)

	// The factory must know about the GitLab provider; the factory's
	// Create should succeed.
	_, err = authManager.GetProvider("gitlab")
	assert.NoError(t, err)
}

// TestSetupProductionAuth_WithForgejoConfig is the Forgejo counterpart
// to the GitLab test.
func TestSetupProductionAuth_WithForgejoConfig(t *testing.T) {
	t.Setenv("TOKEN_ENCRYPTION_KEY", "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI=")
	t.Setenv("FORGEJO_CLIENT_ID", "test-client-id")
	t.Setenv("FORGEJO_CLIENT_SECRET", "test-client-secret")
	t.Setenv("FORGEJO_REDIRECT_URL", "http://localhost:8080/auth/callback?provider=forgejo")

	db := newAuthSetupDB(t)
	authManager, middleware, sessionManager, err := setupProductionAuth(db)
	require.NoError(t, err)
	require.NotNil(t, authManager)
	require.NotNil(t, middleware)
	require.NotNil(t, sessionManager)

	_, err = authManager.GetProvider("forgejo")
	assert.NoError(t, err)
}
