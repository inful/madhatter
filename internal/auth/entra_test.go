package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestEntraProvider_GetAuthURL_IncludesScopes(t *testing.T) {
	t.Parallel()

	p := NewEntraProvider(ProviderConfig{
		ClientID:     "cid",
		ClientSecret: "secret",
		RedirectURL:  "https://example.com/callback",
		AuthURL:      "https://login.example.com/authorize",
		TokenURL:     "https://login.example.com/token",
		UserInfoURL:  "https://graph.example.com/me",
		Scope:        "openid profile email offline_access User.Read",
	})

	authURL := p.GetAuthURL("state")
	u, err := url.Parse(authURL)
	require.NoError(t, err)

	q := u.Query()
	require.Equal(t, "cid", q.Get("client_id"))
	require.NotEmpty(t, q.Get("redirect_uri"))
	require.Contains(t, q.Get("scope"), "openid")
	require.Contains(t, q.Get("scope"), "User.Read")
}

func TestEntraProvider_GetUserInfo_UsesGraphMe(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("Authorization"), "Bearer testtoken"; got != want {
			t.Errorf("Authorization header = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":                "abc",
			"displayName":       "Alice",
			"mail":              "alice@example.com",
			"userPrincipalName": "alice@tenant.example.com",
		})
	}))
	t.Cleanup(srv.Close)

	p := NewEntraProvider(ProviderConfig{
		ClientID:     "cid",
		ClientSecret: "secret",
		RedirectURL:  "https://example.com/callback",
		AuthURL:      "https://login.example.com/authorize",
		TokenURL:     "https://login.example.com/token",
		UserInfoURL:  srv.URL,
		Scope:        "User.Read",
	})

	info, err := p.GetUserInfo(context.Background(), &oauth2.Token{AccessToken: "testtoken"})
	require.NoError(t, err)
	require.Equal(t, "abc", info.ID)
	require.Equal(t, "alice@example.com", info.Email)
	require.Equal(t, "Alice", info.Name)
	require.Equal(t, "alice@tenant.example.com", info.Username)
}
