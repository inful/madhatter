package auth

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/database/sqlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) *database.DB {
	t.Helper()

	// Create temp directory for test database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := database.New(dbPath)
	require.NoError(t, err)

	return db
}

func TestUserService_GetOrCreateUser(t *testing.T) {
	db := setupTestDB(t)
	t.Setenv("TOKEN_ENCRYPTION_KEY", "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI=")

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	userService := NewUserService(db.GetQueries(), encryptor)
	ctx := context.Background()

	t.Run("Create First User - Should Be Admin", func(t *testing.T) {
		userInfo := &UserInfo{
			ID:    "provider-id-1",
			Email: "user1@example.com",
			Name:  "User One",
		}

		user, err := userService.GetOrCreateUser(ctx, userInfo, "forgejo")
		require.NoError(t, err)
		assert.NotEmpty(t, user.ID)
		assert.Equal(t, userInfo.Email, user.Email)
		assert.Equal(t, userInfo.Name, user.Name)
		assert.Equal(t, "forgejo", user.Provider)
		assert.True(t, IsAdmin(user.IsAdmin), "First user should be admin")
	})

	t.Run("Create Second User - Should Not Be Admin", func(t *testing.T) {
		userInfo := &UserInfo{
			ID:    "provider-id-2",
			Email: "user2@example.com",
			Name:  "User Two",
		}

		user, err := userService.GetOrCreateUser(ctx, userInfo, "gitlab")
		require.NoError(t, err)
		assert.NotEmpty(t, user.ID)
		assert.Equal(t, userInfo.Email, user.Email)
		assert.False(t, IsAdmin(user.IsAdmin), "Second user should not be admin")
	})

	t.Run("Get Existing User By Provider", func(t *testing.T) {
		userInfo := &UserInfo{
			ID:    "provider-id-1",
			Email: "user1@example.com",
			Name:  "User One Updated",
		}

		user, err := userService.GetOrCreateUser(ctx, userInfo, "forgejo")
		require.NoError(t, err)
		assert.Equal(t, "user1@example.com", user.Email)
		// Should still be admin
		assert.True(t, IsAdmin(user.IsAdmin))
	})

	t.Run("Same Email Different Provider - Should Error", func(t *testing.T) {
		userInfo := &UserInfo{
			ID:    "provider-id-3",
			Email: "user1@example.com", // Same email as first user
			Name:  "User One",
		}

		_, err := userService.GetOrCreateUser(ctx, userInfo, "gitlab") // Different provider
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already exists with provider")
	})

	t.Run("Same Email Fake Provider - Should Reuse Existing User", func(t *testing.T) {
		userInfo := &UserInfo{
			ID:    "dev-selected-user",
			Email: "user1@example.com", // Existing user from forgejo
			Name:  "User One",
		}

		user, err := userService.GetOrCreateUser(ctx, userInfo, "fake")
		require.NoError(t, err)
		assert.Equal(t, "user1@example.com", user.Email)
		assert.Equal(t, "forgejo", user.Provider)
	})
}

func TestUserService_EnsureTeamMember_PreservesExistingName(t *testing.T) {
	db := setupTestDB(t)
	t.Setenv("TOKEN_ENCRYPTION_KEY", "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI=")

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	userService := NewUserService(db.GetQueries(), encryptor)
	ctx := context.Background()

	_, err = db.AddTeamMember(ctx, "Local Custom Name", "dev@example.com")
	require.NoError(t, err)

	err = userService.EnsureTeamMember(ctx, &UserInfo{
		ID:    "provider-user-1",
		Email: "dev@example.com",
		Name:  "OAuth Provider Name",
	}, true)
	require.NoError(t, err)

	member, err := db.GetMemberByEmail(ctx, "dev@example.com")
	require.NoError(t, err)
	assert.Equal(t, "Local Custom Name", member.Name)
	assert.True(t, member.IsActive)
}

func TestUserService_OAuthTokens(t *testing.T) {
	db := setupTestDB(t)
	t.Setenv("TOKEN_ENCRYPTION_KEY", "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI=")

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	userService := NewUserService(db.GetQueries(), encryptor)
	ctx := context.Background()

	// Create a test user
	userInfo := &UserInfo{
		ID:    "provider-id-1",
		Email: "user@example.com",
		Name:  "Test User",
	}
	user, err := userService.GetOrCreateUser(ctx, userInfo, "forgejo")
	require.NoError(t, err)

	t.Run("Store and Retrieve OAuth Token", func(t *testing.T) {
		token := &sqlc.OauthToken{
			AccessToken:  "access-token-123",
			RefreshToken: sql.NullString{String: "refresh-token-456", Valid: true},
			TokenType:    sql.NullString{String: "Bearer", Valid: true},
			ExpiresAt:    sql.NullTime{Time: time.Now().Add(1 * time.Hour), Valid: true},
		}

		// Store token
		err := userService.StoreOAuthToken(ctx, user.ID, "forgejo", token)
		require.NoError(t, err)

		// Retrieve token
		retrieved, err := userService.GetOAuthToken(ctx, user.ID, "forgejo")
		require.NoError(t, err)
		assert.Equal(t, token.AccessToken, retrieved.AccessToken)
		assert.Equal(t, token.RefreshToken.String, retrieved.RefreshToken.String)
		assert.Equal(t, token.TokenType.String, retrieved.TokenType.String)
	})

	t.Run("Update OAuth Token", func(t *testing.T) {
		token := &sqlc.OauthToken{
			AccessToken:  "new-access-token-789",
			RefreshToken: sql.NullString{String: "new-refresh-token-012", Valid: true},
			TokenType:    sql.NullString{String: "Bearer", Valid: true},
			ExpiresAt:    sql.NullTime{Time: time.Now().Add(2 * time.Hour), Valid: true},
		}

		// Update token
		err := userService.StoreOAuthToken(ctx, user.ID, "forgejo", token)
		require.NoError(t, err)

		// Retrieve updated token
		retrieved, err := userService.GetOAuthToken(ctx, user.ID, "forgejo")
		require.NoError(t, err)
		assert.Equal(t, token.AccessToken, retrieved.AccessToken)
		assert.Equal(t, token.RefreshToken.String, retrieved.RefreshToken.String)
	})

	t.Run("Token Not Found", func(t *testing.T) {
		_, err := userService.GetOAuthToken(ctx, user.ID, "nonexistent")
		assert.Error(t, err)
	})
}

func TestSessionManager_Integration(t *testing.T) {
	db := setupTestDB(t)
	t.Setenv("TOKEN_ENCRYPTION_KEY", "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI=")

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	userService := NewUserService(db.GetQueries(), encryptor)
	sessionManager := NewSessionManager(db.GetQueries(), 24*time.Hour)
	ctx := context.Background()

	// Create a test user
	userInfo := &UserInfo{
		ID:    "test-provider-id",
		Email: "testuser@example.com",
		Name:  "Test User",
	}
	user, err := userService.GetOrCreateUser(ctx, userInfo, "forgejo")
	require.NoError(t, err)

	t.Run("Create and Validate Session", func(t *testing.T) {
		// Create session
		token, err := sessionManager.CreateSession(ctx, user.ID)
		require.NoError(t, err)
		assert.NotEmpty(t, token)

		// Validate session
		session, err := sessionManager.ValidateSession(ctx, token)
		require.NoError(t, err)
		assert.Equal(t, user.ID, session.UserID)
		assert.NotEmpty(t, session.ID)
	})

	t.Run("Invalid Session Token", func(t *testing.T) {
		_, err := sessionManager.ValidateSession(ctx, "invalid-token")
		require.Error(t, err)
		assert.Equal(t, ErrInvalidSession, err)
	})

	t.Run("Destroy Session", func(t *testing.T) {
		// Create session
		token, err := sessionManager.CreateSession(ctx, user.ID)
		require.NoError(t, err)

		// Validate it exists
		_, err = sessionManager.ValidateSession(ctx, token)
		require.NoError(t, err)

		// Destroy session
		err = sessionManager.DestroySession(ctx, token)
		require.NoError(t, err)

		// Should no longer be valid
		_, err = sessionManager.ValidateSession(ctx, token)
		assert.Error(t, err)
	})

	t.Run("Expired Session", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			// Create session with very short duration (1 second)
			shortSessionManager := NewSessionManager(db.GetQueries(), 1*time.Second)
			token, err := shortSessionManager.CreateSession(ctx, user.ID)
			require.NoError(t, err)

			// Wait for expiration plus buffer
			time.Sleep(3 * time.Second)

			// Should no longer be valid (query checks expires_at)
			_, err = shortSessionManager.ValidateSession(ctx, token)
			assert.Error(t, err, "Expired session should not validate")
		})
	})
}

func TestUserService_IsAdmin(t *testing.T) {
	db := setupTestDB(t)
	t.Setenv("TOKEN_ENCRYPTION_KEY", "MTIzNDU2Nzg5MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTI=")

	encryptor, err := NewTokenEncryptor()
	require.NoError(t, err)

	userService := NewUserService(db.GetQueries(), encryptor)
	ctx := context.Background()

	// Create admin user
	adminInfo := &UserInfo{
		ID:    "admin-id",
		Email: "admin@example.com",
		Name:  "Admin User",
	}
	admin, err := userService.GetOrCreateUser(ctx, adminInfo, "forgejo")
	require.NoError(t, err)

	// Create regular user
	userInfo := &UserInfo{
		ID:    "user-id",
		Email: "user@example.com",
		Name:  "Regular User",
	}
	user, err := userService.GetOrCreateUser(ctx, userInfo, "forgejo")
	require.NoError(t, err)

	t.Run("Check Admin User", func(t *testing.T) {
		isAdmin, err := userService.IsAdmin(ctx, admin.ID)
		require.NoError(t, err)
		assert.True(t, isAdmin)
	})

	t.Run("Check Regular User", func(t *testing.T) {
		isAdmin, err := userService.IsAdmin(ctx, user.ID)
		require.NoError(t, err)
		assert.False(t, isAdmin)
	})

	t.Run("Non-existent User", func(t *testing.T) {
		_, err := userService.IsAdmin(ctx, "nonexistent-id")
		assert.Error(t, err)
	})
}
