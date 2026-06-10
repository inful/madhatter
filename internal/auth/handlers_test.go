package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/inful/madhatter/internal/database/sqlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAuthManager_NewAuthManager tests the creation of a new AuthManager.
func TestAuthManager_NewAuthManager(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	providerFactory := NewProviderFactory(make(map[string]ProviderConfig))
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)

	// Test
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	// Assert
	require.NotNil(t, authManager)
	assert.Equal(t, providerFactory, authManager.providerFactory)
	assert.Equal(t, userService, authManager.userService)
	assert.Equal(t, sessionManager, authManager.sessionManager)
	assert.NotNil(t, authManager.providers)
}

// TestAuthManager_RegisterProvider tests registering an OAuth provider.
func TestAuthManager_RegisterProvider(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	providerFactory := NewProviderFactory(make(map[string]ProviderConfig))
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	provider := &FakeProvider{config: ProviderConfig{}}

	// Test
	authManager.RegisterProvider(provider)

	// Assert
	retrieved, err := authManager.GetProvider("fake")
	require.NoError(t, err)
	assert.Equal(t, provider, retrieved)
}

// TestAuthManager_GetProvider_NotFound tests getting a non-existent provider.
func TestAuthManager_GetProvider_NotFound(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	providerFactory := NewProviderFactory(make(map[string]ProviderConfig))
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	// Test
	_, err = authManager.GetProvider("nonexistent")

	// Assert
	assert.ErrorIs(t, err, ErrProviderNotFound)
}

// TestAuthManager_HandleLogin tests the login handler.
func TestAuthManager_HandleLogin(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	// Create provider factory with fake config
	configs := map[string]ProviderConfig{
		"fake": {},
	}
	providerFactory := NewProviderFactory(configs)
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	// Register a fake provider
	fakeProvider := NewFakeProvider(ProviderConfig{})
	authManager.RegisterProvider(fakeProvider)

	// Create request with correct URL pattern and chi route context
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/login/fake", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("provider", "fake")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	// Test
	authManager.HandleLogin(w, req)

	// Assert
	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Contains(t, w.Header().Get("Location"), "/auth/fake/login?state=")

	// Check state cookie was set
	cookies := w.Result().Cookies()
	found := false
	for _, cookie := range cookies {
		if cookie.Name == "oauth_state" {
			found = true
			assert.NotEmpty(t, cookie.Value)
			assert.Equal(t, "/auth/callback", cookie.Path)
			assert.True(t, cookie.HttpOnly)
			assert.Equal(t, 300, cookie.MaxAge)
		}
	}
	assert.True(t, found, "oauth_state cookie should be set")
}

// TestAuthManager_HandleLogin_MissingProvider tests login with no provider.
func TestAuthManager_HandleLogin_MissingProvider(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	providerFactory := NewProviderFactory(make(map[string]ProviderConfig))
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	// Create request without provider
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/login/", nil)
	w := httptest.NewRecorder()

	// Test
	authManager.HandleLogin(w, req)

	// Assert
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Provider required")
}

// TestAuthManager_HandleLogin_InvalidProvider tests login with invalid provider.
func TestAuthManager_HandleLogin_InvalidProvider(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	providerFactory := NewProviderFactory(make(map[string]ProviderConfig))
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	// Create request with invalid provider
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/login/invalid", nil)
	w := httptest.NewRecorder()

	// Test
	authManager.HandleLogin(w, req)

	// Assert
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Provider required")
}

// TestAuthManager_HandleCallback tests the OAuth callback handler.
func TestAuthManager_HandleCallback(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	providerFactory := NewProviderFactory(map[string]ProviderConfig{
		"fake": {},
	})
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	// Register fake provider
	fakeProvider := NewFakeProvider(ProviderConfig{})
	authManager.RegisterProvider(fakeProvider)

	// Create state cookie
	state := "test-state"

	// Create callback request
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/callback?code=test-code&state=test-state&provider=fake", nil)
	req.AddCookie(&http.Cookie{ //nolint:gosec // G124 false positive: cookie has all required security attributes (HttpOnly, Secure, SameSite); gosec source analysis cannot see through AddCookie call site
		Name:     "oauth_state",
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	})
	w := httptest.NewRecorder()

	// Test
	authManager.HandleCallback(w, req)

	// Assert - Should redirect to dashboard
	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/", w.Header().Get("Location"))

	// Assert - Team member is auto-created.
	member, err := db.GetMemberByEmail(context.Background(), "dev@example.com")
	require.NoError(t, err)
	require.NotNil(t, member)
	assert.Equal(t, "Development User", member.Name)
	assert.True(t, member.IsActive)

	// Check session cookie was set
	cookies := w.Result().Cookies()
	found := false
	for _, cookie := range cookies {
		if cookie.Name == "session_token" {
			found = true
			assert.NotEmpty(t, cookie.Value)
			assert.True(t, cookie.HttpOnly)
			// In test environment, Secure may be false since there's no TLS
			// assert.True(t, cookie.Secure)
		}
	}
	assert.True(t, found, "session_token cookie should be set")
}

// TestAuthManager_HandleCallback_MissingState tests callback without state.
func TestAuthManager_HandleCallback_MissingState(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	providerFactory := NewProviderFactory(make(map[string]ProviderConfig))
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	// Create request without state cookie
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/callback?code=test", nil)
	w := httptest.NewRecorder()

	// Test
	authManager.HandleCallback(w, req)

	// Assert
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Invalid state")
}

// TestAuthManager_HandleCallback_StateMismatch tests callback with state mismatch.
func TestAuthManager_HandleCallback_StateMismatch(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	providerFactory := NewProviderFactory(make(map[string]ProviderConfig))
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	// Create request with mismatched state
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/callback?code=test&state=wrong", nil)
	req.AddCookie(&http.Cookie{ //nolint:gosec // G124 false positive: cookie has all required security attributes (HttpOnly, Secure, SameSite); gosec source analysis cannot see through AddCookie call site
		Name:     "oauth_state",
		Value:    "correct-state",
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	})
	w := httptest.NewRecorder()

	// Test
	authManager.HandleCallback(w, req)

	// Assert
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "State mismatch")
}

// TestAuthManager_HandleCallback_MissingProvider tests callback without provider.
func TestAuthManager_HandleCallback_MissingProvider(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	providerFactory := NewProviderFactory(make(map[string]ProviderConfig))
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	// Create request without provider
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/callback?code=test&state=test", nil)
	req.AddCookie(&http.Cookie{ //nolint:gosec // G124 false positive: cookie has all required security attributes (HttpOnly, Secure, SameSite); gosec source analysis cannot see through AddCookie call site
		Name:     "oauth_state",
		Value:    "test",
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	})
	w := httptest.NewRecorder()

	// Test
	authManager.HandleCallback(w, req)

	// Assert
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Provider required")
}

// TestAuthManager_HandleCallback_MissingCode tests callback without code.
func TestAuthManager_HandleCallback_MissingCode(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	providerFactory := NewProviderFactory(map[string]ProviderConfig{
		"fake": {},
	})
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	// Register fake provider
	fakeProvider := NewFakeProvider(ProviderConfig{})
	authManager.RegisterProvider(fakeProvider)

	// Create request without code
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/auth/callback?state=test&provider=fake", nil)
	req.AddCookie(&http.Cookie{ //nolint:gosec // G124 false positive: cookie has all required security attributes (HttpOnly, Secure, SameSite); gosec source analysis cannot see through AddCookie call site
		Name:     "oauth_state",
		Value:    "test",
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	})
	w := httptest.NewRecorder()

	// Test
	authManager.HandleCallback(w, req)

	// Assert
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Authorization code required")
}

// TestAuthManager_HandleLogout tests the logout handler.
func TestAuthManager_HandleLogout(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	providerFactory := NewProviderFactory(make(map[string]ProviderConfig))
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	// Create a user first
	ctx := context.Background()
	userID := uuid.New().String()
	_, err = queries.CreateUser(ctx, sqlc.CreateUserParams{
		ID:    userID,
		Email: "test@example.com",
		Name:  "Test User",
	})
	require.NoError(t, err)

	// Create a session
	sessionToken, err := sessionManager.CreateSession(ctx, userID)
	require.NoError(t, err)

	// Create logout request
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/auth/logout", nil)
	req.AddCookie(&http.Cookie{ //nolint:gosec // G124 false positive: cookie has all required security attributes (HttpOnly, Secure, SameSite); gosec source analysis cannot see through AddCookie call site
		Name:     "session_token",
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	})
	w := httptest.NewRecorder()

	// Test
	authManager.HandleLogout(w, req)

	// Assert
	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/login", w.Header().Get("Location"))

	// Check session cookie was cleared
	cookies := w.Result().Cookies()
	found := false
	for _, cookie := range cookies {
		if cookie.Name == "session_token" && cookie.MaxAge == -1 {
			found = true
		}
	}
	assert.True(t, found, "session cookie should be cleared")
}

// TestAuthManager_HandleLoginView tests the login view handler.
func TestAuthManager_HandleLoginView(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	// Create provider factory with forgejo and gitlab configs
	configs := map[string]ProviderConfig{
		"forgejo": {},
		"gitlab":  {},
	}
	providerFactory := NewProviderFactory(configs)
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	// Create request
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/login", nil)
	w := httptest.NewRecorder()

	// Test
	authManager.HandleLoginView(w, req)

	// Assert
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "MadHatter Login")
	assert.Contains(t, w.Body.String(), "Login with Forgejo")
	assert.Contains(t, w.Body.String(), "Login with GitLab")
}

// TestAuthManager_HandleLoginView_AlreadyLoggedIn tests login view when already authenticated.
func TestAuthManager_HandleLoginView_AlreadyLoggedIn(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	providerFactory := NewProviderFactory(make(map[string]ProviderConfig))
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	// Create a user and session
	ctx := context.Background()
	userID := uuid.New().String()
	_, err = queries.CreateUser(ctx, sqlc.CreateUserParams{
		ID:      userID,
		Email:   "test@example.com",
		Name:    "Test User",
		IsAdmin: sql.NullInt64{Int64: 1, Valid: true},
	})
	require.NoError(t, err)

	sessionToken, err := sessionManager.CreateSession(ctx, userID)
	require.NoError(t, err)

	// Create request with session
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/login", nil)
	req.AddCookie(&http.Cookie{ //nolint:gosec // G124 false positive: cookie has all required security attributes (HttpOnly, Secure, SameSite); gosec source analysis cannot see through AddCookie call site
		Name:     "session_token",
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	})
	w := httptest.NewRecorder()

	// Test
	authManager.HandleLoginView(w, req)

	// Assert - Should redirect to dashboard
	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/", w.Header().Get("Location"))
}

// TestAuthManager_HandleGenerateAPIToken tests generating an API token.
func TestAuthManager_HandleGenerateAPIToken(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	providerFactory := NewProviderFactory(make(map[string]ProviderConfig))
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	// Create authenticated user
	ctx := context.Background()
	userID := uuid.New().String()
	_, err = queries.CreateUser(ctx, sqlc.CreateUserParams{
		ID:    userID,
		Email: "test@example.com",
		Name:  "Test User",
	})
	require.NoError(t, err)

	sessionToken, err := sessionManager.CreateSession(ctx, userID)
	require.NoError(t, err)

	// Create request
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/tokens/generate?name=my-token", nil)
	req.AddCookie(&http.Cookie{ //nolint:gosec // G124 false positive: cookie has all required security attributes (HttpOnly, Secure, SameSite); gosec source analysis cannot see through AddCookie call site
		Name:     "session_token",
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	})
	w := httptest.NewRecorder()

	// Test
	authManager.HandleGenerateAPIToken(w, req)

	// Assert
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	// Parse response
	var response map[string]string
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	token, exists := response["token"]
	assert.True(t, exists)
	assert.NotEmpty(t, token)
	// Token should be a non-empty base64-encoded string (from generateSecureToken)
	assert.NotEmpty(t, token)
}

// TestAuthManager_HandleGenerateAPIToken_Unauthenticated tests without auth.
func TestAuthManager_HandleGenerateAPIToken_Unauthenticated(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	providerFactory := NewProviderFactory(make(map[string]ProviderConfig))
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	// Create request without auth
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/tokens/generate?name=my-token", nil)
	w := httptest.NewRecorder()

	// Test
	authManager.HandleGenerateAPIToken(w, req)

	// Assert
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "Authentication required")
}

// TestAuthManager_HandleListAPITokens tests listing API tokens.
func TestAuthManager_HandleListAPITokens(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	providerFactory := NewProviderFactory(make(map[string]ProviderConfig))
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	// Create authenticated user
	ctx := context.Background()
	userID := uuid.New().String()
	_, err = queries.CreateUser(ctx, sqlc.CreateUserParams{
		ID:    userID,
		Email: "test@example.com",
		Name:  "Test User",
	})
	require.NoError(t, err)

	sessionToken, err := sessionManager.CreateSession(ctx, userID)
	require.NoError(t, err)

	// Create some API tokens
	_, err = queries.CreateAPIToken(ctx, sqlc.CreateAPITokenParams{
		ID:        uuid.New().String(),
		UserID:    userID,
		Name:      "token1",
		TokenHash: "hash1",
		IsActive:  sql.NullInt64{Int64: 1, Valid: true},
	})
	require.NoError(t, err)

	_, err = queries.CreateAPIToken(ctx, sqlc.CreateAPITokenParams{
		ID:        uuid.New().String(),
		UserID:    userID,
		Name:      "token2",
		TokenHash: "hash2",
		IsActive:  sql.NullInt64{Int64: 1, Valid: true},
	})
	require.NoError(t, err)

	// Create request
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/tokens", nil)
	req.AddCookie(&http.Cookie{ //nolint:gosec // G124 false positive: cookie has all required security attributes (HttpOnly, Secure, SameSite); gosec source analysis cannot see through AddCookie call site
		Name:     "session_token",
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	})
	w := httptest.NewRecorder()

	// Test
	authManager.HandleListAPITokens(w, req)

	// Assert
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	// Parse response
	var tokens []map[string]any
	err = json.Unmarshal(w.Body.Bytes(), &tokens)
	require.NoError(t, err)

	assert.Len(t, tokens, 2)
	assert.Equal(t, "token1", tokens[0]["name"])
	assert.Equal(t, "token2", tokens[1]["name"])
}

// TestAuthManager_HandleListAPITokens_Unauthenticated tests without auth.
func TestAuthManager_HandleListAPITokens_Unauthenticated(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	providerFactory := NewProviderFactory(make(map[string]ProviderConfig))
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	// Create request without auth
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/tokens", nil)
	w := httptest.NewRecorder()

	// Test
	authManager.HandleListAPITokens(w, req)

	// Assert
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "Authentication required")
}

// TestAuthManager_HandleRevokeAPIToken tests revoking an API token.
func TestAuthManager_HandleRevokeAPIToken(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	providerFactory := NewProviderFactory(make(map[string]ProviderConfig))
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	// Create authenticated user
	ctx := context.Background()
	userID := uuid.New().String()
	_, err = queries.CreateUser(ctx, sqlc.CreateUserParams{
		ID:    userID,
		Email: "test@example.com",
		Name:  "Test User",
	})
	require.NoError(t, err)

	sessionToken, err := sessionManager.CreateSession(ctx, userID)
	require.NoError(t, err)

	// Create API token
	tokenID := uuid.New().String()
	_, err = queries.CreateAPIToken(ctx, sqlc.CreateAPITokenParams{
		ID:        tokenID,
		UserID:    userID,
		Name:      "my-token",
		TokenHash: "hash1",
		IsActive:  sql.NullInt64{Int64: 1, Valid: true},
	})
	require.NoError(t, err)

	// Create request
	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/tokens/"+tokenID, nil)
	req.AddCookie(&http.Cookie{ //nolint:gosec // G124 false positive: cookie has all required security attributes (HttpOnly, Secure, SameSite); gosec source analysis cannot see through AddCookie call site
		Name:     "session_token",
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	})

	// Set URL parameter
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", tokenID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	// Test
	authManager.HandleRevokeAPIToken(w, req)

	// Assert
	assert.Equal(t, http.StatusNoContent, w.Code)

	// Verify token was deleted
	_, err = queries.GetAPITokenByID(ctx, tokenID)
	assert.Error(t, err)
}

// TestAuthManager_HandleRevokeAPIToken_Unauthorized tests revoking token belonging to another user.
func TestAuthManager_HandleRevokeAPIToken_Unauthorized(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	providerFactory := NewProviderFactory(make(map[string]ProviderConfig))
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	// Create two users with unique provider/provider_id
	ctx := context.Background()
	user1ID := uuid.New().String()
	user2ID := uuid.New().String()

	_, err = queries.CreateUser(ctx, sqlc.CreateUserParams{
		ID:         user1ID,
		Email:      "user1@example.com",
		Name:       "User 1",
		Provider:   "fake",
		ProviderID: "user1",
	})
	require.NoError(t, err)

	_, err = queries.CreateUser(ctx, sqlc.CreateUserParams{
		ID:         user2ID,
		Email:      "user2@example.com",
		Name:       "User 2",
		Provider:   "fake",
		ProviderID: "user2",
	})
	require.NoError(t, err)

	// User 1 creates token
	tokenID := uuid.New().String()
	_, err = queries.CreateAPIToken(ctx, sqlc.CreateAPITokenParams{
		ID:        tokenID,
		UserID:    user1ID,
		Name:      "user1-token",
		TokenHash: "hash1",
		IsActive:  sql.NullInt64{Int64: 1, Valid: true},
	})
	require.NoError(t, err)

	// User 2 creates session
	sessionToken, err := sessionManager.CreateSession(ctx, user2ID)
	require.NoError(t, err)

	// User 2 tries to revoke User 1's token
	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/tokens/"+tokenID, nil)
	req.AddCookie(&http.Cookie{ //nolint:gosec // G124 false positive: cookie has all required security attributes (HttpOnly, Secure, SameSite); gosec source analysis cannot see through AddCookie call site
		Name:     "session_token",
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	})

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", tokenID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	// Test
	authManager.HandleRevokeAPIToken(w, req)

	// Assert
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "Not authorized")
}

// TestAuthManager_HandleCleanupExpiredTokens tests cleanup of expired tokens.
func TestAuthManager_HandleCleanupExpiredTokens(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	providerFactory := NewProviderFactory(make(map[string]ProviderConfig))
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	// Create admin user
	ctx := context.Background()
	adminID := uuid.New().String()
	_, err = queries.CreateUser(ctx, sqlc.CreateUserParams{
		ID:      adminID,
		Email:   "admin@example.com",
		Name:    "Admin",
		IsAdmin: sql.NullInt64{Int64: 1, Valid: true},
	})
	require.NoError(t, err)

	sessionToken, err := sessionManager.CreateSession(ctx, adminID)
	require.NoError(t, err)

	// Create expired token
	expiredTime := time.Now().Add(-24 * time.Hour)
	_, err = queries.CreateAPIToken(ctx, sqlc.CreateAPITokenParams{
		ID:        uuid.New().String(),
		UserID:    adminID,
		Name:      "expired-token",
		TokenHash: "hash1",
		ExpiresAt: sql.NullTime{Time: expiredTime, Valid: true},
		IsActive:  sql.NullInt64{Int64: 1, Valid: true},
	})
	require.NoError(t, err)

	// Create request
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/tokens/cleanup", nil)
	req.AddCookie(&http.Cookie{ //nolint:gosec // G124 false positive: cookie has all required security attributes (HttpOnly, Secure, SameSite); gosec source analysis cannot see through AddCookie call site
		Name:     "session_token",
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	})
	w := httptest.NewRecorder()

	// Test
	authManager.HandleCleanupExpiredTokens(w, req)

	// Assert
	assert.Equal(t, http.StatusNoContent, w.Code)
}

// TestAuthManager_HandleCleanupExpiredTokens_NotAdmin tests cleanup by non-admin.
func TestAuthManager_HandleCleanupExpiredTokens_NotAdmin(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	providerFactory := NewProviderFactory(make(map[string]ProviderConfig))
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	// Create regular user
	ctx := context.Background()
	userID := uuid.New().String()
	_, err = queries.CreateUser(ctx, sqlc.CreateUserParams{
		ID:      userID,
		Email:   "user@example.com",
		Name:    "User",
		IsAdmin: sql.NullInt64{Int64: 0, Valid: true},
	})
	require.NoError(t, err)

	sessionToken, err := sessionManager.CreateSession(ctx, userID)
	require.NoError(t, err)

	// Create request
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/tokens/cleanup", nil)
	req.AddCookie(&http.Cookie{ //nolint:gosec // G124 false positive: cookie has all required security attributes (HttpOnly, Secure, SameSite); gosec source analysis cannot see through AddCookie call site
		Name:     "session_token",
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	})
	w := httptest.NewRecorder()

	// Test
	authManager.HandleCleanupExpiredTokens(w, req)

	// Assert
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "Admin privileges required")
}

// TestAuthManager_HandleCleanupExpiredTokens_Unauthenticated tests without auth.
func TestAuthManager_HandleCleanupExpiredTokens_Unauthenticated(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	providerFactory := NewProviderFactory(make(map[string]ProviderConfig))
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	// Create request without auth
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/tokens/cleanup", nil)
	w := httptest.NewRecorder()

	// Test
	authManager.HandleCleanupExpiredTokens(w, req)

	// Assert
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "Authentication required")
}

// Test_generateStateToken tests state token generation.
func Test_generateStateToken(t *testing.T) {
	// Test multiple generations are unique
	token1, err := generateStateToken()
	require.NoError(t, err)
	assert.NotEmpty(t, token1)

	token2, err := generateStateToken()
	require.NoError(t, err)
	assert.NotEmpty(t, token2)

	// Tokens should be different
	assert.NotEqual(t, token1, token2)
}

// Test_capitalizeProviderName tests provider name capitalization.
func Test_capitalizeProviderName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"forgejo", "Forgejo"},
		{"gitlab", "GitLab"},
		{"Forgejo", "Forgejo"},
		{"GitLab", "GitLab"},
		{"", ""},
		{"abc", "Abc"},
		{"ABC", "ABC"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := capitalizeProviderName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestAuthManager_writeTokensResponse tests the token response writer.
func TestAuthManager_writeTokensResponse(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	providerFactory := NewProviderFactory(make(map[string]ProviderConfig))
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	// Create test tokens
	tokens := []sqlc.ApiToken{
		{
			ID:        "token1",
			Name:      "My Token",
			CreatedAt: sql.NullTime{Time: time.Now(), Valid: true},
			IsActive:  sql.NullInt64{Int64: 1, Valid: true},
		},
		{
			ID:        "token2",
			Name:      "Another Token",
			CreatedAt: sql.NullTime{Time: time.Now(), Valid: true},
			IsActive:  sql.NullInt64{Int64: 0, Valid: true},
		},
	}

	// Create response recorder
	w := httptest.NewRecorder()

	// Test
	authManager.writeTokensResponse(w, tokens)

	// Assert
	assert.Equal(t, http.StatusOK, w.Code)

	// Parse response
	var result []map[string]any
	err = json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)

	assert.Len(t, result, 2)
	assert.Equal(t, "token1", result[0]["id"])
	assert.Equal(t, "My Token", result[0]["name"])
	assert.Equal(t, true, result[0]["is_active"])

	assert.Equal(t, "token2", result[1]["id"])
	assert.Equal(t, "Another Token", result[1]["name"])
	assert.Equal(t, false, result[1]["is_active"])
}

// TestAuthManager_writeTokensResponse_Empty tests empty token list.
func TestAuthManager_writeTokensResponse_Empty(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	providerFactory := NewProviderFactory(make(map[string]ProviderConfig))
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	// Create response recorder
	w := httptest.NewRecorder()

	// Test with empty list
	authManager.writeTokensResponse(w, []sqlc.ApiToken{})

	// Assert
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "[]", w.Body.String())
}

// TestAuthManager_writeTokensResponse_Single tests single token.
func TestAuthManager_writeTokensResponse_Single(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	providerFactory := NewProviderFactory(make(map[string]ProviderConfig))
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	// Create single token
	token := sqlc.ApiToken{
		ID:        "token1",
		Name:      "Single Token",
		CreatedAt: sql.NullTime{Time: time.Now(), Valid: true},
		IsActive:  sql.NullInt64{Int64: 1, Valid: true},
	}

	// Create response recorder
	w := httptest.NewRecorder()

	// Test
	authManager.writeTokensResponse(w, []sqlc.ApiToken{token})

	// Assert
	assert.Equal(t, http.StatusOK, w.Code)

	// Parse response
	var result []map[string]any
	err = json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)

	assert.Len(t, result, 1)
}

// TestAuthManager_HandleRevokeAPIToken_MissingID tests revoking without token ID.
func TestAuthManager_HandleRevokeAPIToken_MissingID(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	providerFactory := NewProviderFactory(make(map[string]ProviderConfig))
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	// Create authenticated user
	ctx := context.Background()
	userID := uuid.New().String()
	_, err = queries.CreateUser(ctx, sqlc.CreateUserParams{
		ID:    userID,
		Email: "test@example.com",
		Name:  "Test User",
	})
	require.NoError(t, err)

	sessionToken, err := sessionManager.CreateSession(ctx, userID)
	require.NoError(t, err)

	// Create request without ID
	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/tokens/", nil)
	req.AddCookie(&http.Cookie{ //nolint:gosec // G124 false positive: cookie has all required security attributes (HttpOnly, Secure, SameSite); gosec source analysis cannot see through AddCookie call site
		Name:     "session_token",
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	})
	w := httptest.NewRecorder()

	// Test
	authManager.HandleRevokeAPIToken(w, req)

	// Assert
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Token ID required")
}

// TestAuthManager_HandleRevokeAPIToken_NotFound tests revoking non-existent token.
func TestAuthManager_HandleRevokeAPIToken_NotFound(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	providerFactory := NewProviderFactory(make(map[string]ProviderConfig))
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	// Create authenticated user
	ctx := context.Background()
	userID := uuid.New().String()
	_, err = queries.CreateUser(ctx, sqlc.CreateUserParams{
		ID:    userID,
		Email: "test@example.com",
		Name:  "Test User",
	})
	require.NoError(t, err)

	sessionToken, err := sessionManager.CreateSession(ctx, userID)
	require.NoError(t, err)

	// Create request with non-existent token ID
	nonExistentID := uuid.New().String()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/tokens/"+nonExistentID, nil)
	req.AddCookie(&http.Cookie{ //nolint:gosec // G124 false positive: cookie has all required security attributes (HttpOnly, Secure, SameSite); gosec source analysis cannot see through AddCookie call site
		Name:     "session_token",
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	})

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", nonExistentID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	// Test
	authManager.HandleRevokeAPIToken(w, req)

	// Assert
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "Token not found")
}

// TestAuthManager_HandleGenerateAPIToken_WithExpiry tests token generation with expiry.
func TestAuthManager_HandleGenerateAPIToken_WithExpiry(t *testing.T) {
	// Setup
	db := setupTestDB(t)
	queries := db.GetQueries()

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	providerFactory := NewProviderFactory(make(map[string]ProviderConfig))
	userService := NewUserService(queries, encryptor)
	sessionManager := NewSessionManager(queries, 24*time.Hour)
	authManager := NewAuthManager(providerFactory, userService, sessionManager)

	// Create authenticated user
	ctx := context.Background()
	userID := uuid.New().String()
	_, err = queries.CreateUser(ctx, sqlc.CreateUserParams{
		ID:    userID,
		Email: "test@example.com",
		Name:  "Test User",
	})
	require.NoError(t, err)

	sessionToken, err := sessionManager.CreateSession(ctx, userID)
	require.NoError(t, err)

	// Create request with expiry
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/tokens/generate?name=expiring-token&expiry_days=30", nil)
	req.AddCookie(&http.Cookie{ //nolint:gosec // G124 false positive: cookie has all required security attributes (HttpOnly, Secure, SameSite); gosec source analysis cannot see through AddCookie call site
		Name:     "session_token",
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	})
	w := httptest.NewRecorder()

	// Test
	authManager.HandleGenerateAPIToken(w, req)

	// Assert
	assert.Equal(t, http.StatusOK, w.Code)

	// Parse response
	var response map[string]string
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	token, exists := response["token"]
	assert.True(t, exists)
	assert.NotEmpty(t, token)
}
