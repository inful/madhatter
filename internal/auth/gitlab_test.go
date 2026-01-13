package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

const (
	testAPIV4User   = "/api/v4/user"
	testAPIV4Groups = "/api/v4/groups"
)

// TestGitLabProvider_GetUserInfo_WithoutGroupRestriction tests user info retrieval without group validation.
func TestGitLabProvider_GetUserInfo_WithoutGroupRestriction(t *testing.T) {
	// Setup mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == testAPIV4User {
			w.Header().Set("Content-Type", "application/json")
			user := map[string]any{
				"id":         int64(123),
				"username":   "testuser",
				"name":       "Test User",
				"email":      "test@example.com",
				"avatar_url": "https://example.com/avatar.jpg",
			}
			if err := json.NewEncoder(w).Encode(user); err != nil {
				t.Fatalf("Failed to encode response: %v", err)
			}
		}
	}))
	defer server.Close()

	// Create provider without group restriction
	config := ProviderConfig{
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		RedirectURL:  "http://localhost/callback",
		AuthURL:      server.URL + "/oauth/authorize",
		TokenURL:     server.URL + "/oauth/token",
		UserInfoURL:  server.URL + testAPIV4User,
		Scope:        "read_user",
		AllowedGroup: "", // No group restriction
	}
	provider := NewGitLabProvider(config)

	// Test
	ctx := context.Background()
	token := &oauth2.Token{AccessToken: "test-token"}
	userInfo, err := provider.GetUserInfo(ctx, token)

	// Assert
	require.NoError(t, err)
	assert.NotNil(t, userInfo)
	assert.Equal(t, "123", userInfo.ID)
	assert.Equal(t, "test@example.com", userInfo.Email)
	assert.Equal(t, "Test User", userInfo.Name)
	assert.Equal(t, "testuser", userInfo.Username)
}

// TestGitLabProvider_GetUserInfo_WithGroupRestriction_Success tests successful authentication with group membership.
func TestGitLabProvider_GetUserInfo_WithGroupRestriction_Success(t *testing.T) {
	// Setup mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case testAPIV4User:
			w.Header().Set("Content-Type", "application/json")
			user := map[string]any{
				"id":         int64(123),
				"username":   "testuser",
				"name":       "Test User",
				"email":      "test@example.com",
				"avatar_url": "https://example.com/avatar.jpg",
			}
			if err := json.NewEncoder(w).Encode(user); err != nil {
				t.Fatalf("Failed to encode response: %v", err)
			}
		case testAPIV4Groups:
			// Mock groups endpoint - user is member of allowed group
			w.Header().Set("Content-Type", "application/json")
			groups := []map[string]any{
				{
					"id":        int64(456),
					"full_path": "myorg/myteam",
				},
			}
			if err := json.NewEncoder(w).Encode(groups); err != nil {
				t.Fatalf("Failed to encode response: %v", err)
			}
		}
	}))
	defer server.Close()

	// Create provider with group restriction
	config := ProviderConfig{
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		RedirectURL:  "http://localhost/callback",
		AuthURL:      server.URL + "/oauth/authorize",
		TokenURL:     server.URL + "/oauth/token",
		UserInfoURL:  server.URL + testAPIV4User,
		Scope:        "read_user",
		AllowedGroup: "myorg/myteam",
	}
	provider := NewGitLabProvider(config)

	// Test
	ctx := context.Background()
	token := &oauth2.Token{AccessToken: "test-token"}
	userInfo, err := provider.GetUserInfo(ctx, token)

	// Assert
	require.NoError(t, err)
	assert.NotNil(t, userInfo)
	assert.Equal(t, "123", userInfo.ID)
	assert.Equal(t, "test@example.com", userInfo.Email)
}

// TestGitLabProvider_GetUserInfo_WithGroupRestriction_Denied tests authentication denial when user is not in group.
func TestGitLabProvider_GetUserInfo_WithGroupRestriction_Denied(t *testing.T) {
	// Setup mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case testAPIV4User:
			w.Header().Set("Content-Type", "application/json")
			user := map[string]any{
				"id":         int64(123),
				"username":   "testuser",
				"name":       "Test User",
				"email":      "test@example.com",
				"avatar_url": "https://example.com/avatar.jpg",
			}
			if err := json.NewEncoder(w).Encode(user); err != nil {
				t.Fatalf("Failed to encode response: %v", err)
			}
		case testAPIV4Groups:
			// Mock groups endpoint - user is NOT member of allowed group
			w.Header().Set("Content-Type", "application/json")
			groups := []map[string]any{
				{
					"id":        int64(789),
					"full_path": "otherorg/otherteam",
				},
			}
			if err := json.NewEncoder(w).Encode(groups); err != nil {
				t.Fatalf("Failed to encode response: %v", err)
			}
		}
	}))
	defer server.Close()

	// Create provider with group restriction
	config := ProviderConfig{
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		RedirectURL:  "http://localhost/callback",
		AuthURL:      server.URL + "/oauth/authorize",
		TokenURL:     server.URL + "/oauth/token",
		UserInfoURL:  server.URL + testAPIV4User,
		Scope:        "read_user",
		AllowedGroup: "myorg/myteam",
	}
	provider := NewGitLabProvider(config)

	// Test
	ctx := context.Background()
	token := &oauth2.Token{AccessToken: "test-token"}
	userInfo, err := provider.GetUserInfo(ctx, token)

	// Assert
	require.Error(t, err)
	assert.Nil(t, userInfo)
	require.ErrorIs(t, err, ErrGroupMembershipDenied)
	assert.Contains(t, err.Error(), "myorg/myteam")
}

// TestGitLabProvider_GetUserInfo_WithGroupRestriction_EmptyGroups tests authentication denial when user has no groups.
func TestGitLabProvider_GetUserInfo_WithGroupRestriction_EmptyGroups(t *testing.T) {
	// Setup mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case testAPIV4User:
			w.Header().Set("Content-Type", "application/json")
			user := map[string]any{
				"id":         int64(123),
				"username":   "testuser",
				"name":       "Test User",
				"email":      "test@example.com",
				"avatar_url": "https://example.com/avatar.jpg",
			}
			if err := json.NewEncoder(w).Encode(user); err != nil {
				t.Fatalf("Failed to encode response: %v", err)
			}
		case testAPIV4Groups:
			// Mock groups endpoint - user has no groups
			w.Header().Set("Content-Type", "application/json")
			groups := []map[string]any{}
			if err := json.NewEncoder(w).Encode(groups); err != nil {
				t.Fatalf("Failed to encode response: %v", err)
			}
		}
	}))
	defer server.Close()

	// Create provider with group restriction
	config := ProviderConfig{
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		RedirectURL:  "http://localhost/callback",
		AuthURL:      server.URL + "/oauth/authorize",
		TokenURL:     server.URL + "/oauth/token",
		UserInfoURL:  server.URL + testAPIV4User,
		Scope:        "read_user",
		AllowedGroup: "myorg/myteam",
	}
	provider := NewGitLabProvider(config)

	// Test
	ctx := context.Background()
	token := &oauth2.Token{AccessToken: "test-token"}
	userInfo, err := provider.GetUserInfo(ctx, token)

	// Assert
	require.Error(t, err)
	assert.Nil(t, userInfo)
	require.ErrorIs(t, err, ErrGroupMembershipDenied)
}

// TestGitLabProvider_checkGroupMembership tests the group membership checking logic directly.
func TestGitLabProvider_checkGroupMembership(t *testing.T) {
	tests := []struct {
		name           string
		allowedGroup   string
		returnedGroups []map[string]any
		expectedResult bool
	}{
		{
			name:         "User is member of allowed group",
			allowedGroup: "myorg/myteam",
			returnedGroups: []map[string]any{
				{"id": int64(1), "full_path": "myorg/myteam"},
				{"id": int64(2), "full_path": "otherorg/team"},
			},
			expectedResult: true,
		},
		{
			name:         "User is not member of allowed group",
			allowedGroup: "myorg/myteam",
			returnedGroups: []map[string]any{
				{"id": int64(1), "full_path": "otherorg/team"},
			},
			expectedResult: false,
		},
		{
			name:           "User has no groups",
			allowedGroup:   "myorg/myteam",
			returnedGroups: []map[string]any{},
			expectedResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup mock server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == testAPIV4Groups {
					w.Header().Set("Content-Type", "application/json")
					if err := json.NewEncoder(w).Encode(tt.returnedGroups); err != nil {
						t.Fatalf("Failed to encode response: %v", err)
					}
				}
			}))
			defer server.Close()

			// Create provider
			config := ProviderConfig{
				ClientID:     "test-client",
				ClientSecret: "test-secret",
				UserInfoURL:  server.URL + testAPIV4User,
				AllowedGroup: tt.allowedGroup,
			}
			provider := NewGitLabProvider(config)

			// Test
			ctx := context.Background()
			token := &oauth2.Token{AccessToken: "test-token"}
			client := provider.oauth.Client(ctx, token)
			isMember, err := provider.checkGroupMembership(ctx, client, tt.allowedGroup)

			// Assert
			require.NoError(t, err)
			assert.Equal(t, tt.expectedResult, isMember)
		})
	}
}
