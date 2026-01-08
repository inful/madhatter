package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/database/sqlc"
)

const (
	// OAuthStateCookieMaxAge is the max age in seconds for the OAuth state cookie (5 minutes).
	oAuthStateCookieMaxAge = 300
)

// AuthManager coordinates authentication operations.
type AuthManager struct {
	providerFactory *ProviderFactory
	userService     *UserService
	sessionManager  *SessionManager
	providers       map[string]Provider
}

// NewAuthManager creates a new authentication manager.
func NewAuthManager(
	providerFactory *ProviderFactory,
	userService *UserService,
	sessionManager *SessionManager,
) *AuthManager {
	return &AuthManager{
		providerFactory: providerFactory,
		userService:     userService,
		sessionManager:  sessionManager,
		providers:       make(map[string]Provider),
	}
}

// RegisterProvider registers an OAuth provider.
func (am *AuthManager) RegisterProvider(provider Provider) {
	am.providers[provider.Name()] = provider
}

// GetProvider returns a registered provider by name.
func (am *AuthManager) GetProvider(name string) (Provider, error) {
	provider, exists := am.providers[name]
	if !exists {
		return nil, ErrProviderNotFound
	}
	return provider, nil
}

// HandleLogin initiates the OAuth2 flow.
func (am *AuthManager) HandleLogin(w http.ResponseWriter, r *http.Request) {
	providerName := chi.URLParam(r, "provider")
	if providerName == "" {
		http.Error(w, "Provider required", http.StatusBadRequest)
		return
	}

	provider, err := am.GetProvider(providerName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Generate state parameter for CSRF protection
	state, err := generateStateToken()
	if err != nil {
		http.Error(w, "Failed to generate state", http.StatusInternalServerError)
		return
	}

	// Determine if the current request is using HTTPS (directly or via proxy)
	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")

	// Store state in cookie (short-lived)
	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		Path:     "/auth/callback",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   oAuthStateCookieMaxAge,
	})

	// Redirect to provider
	authURL := provider.GetAuthURL(state)
	http.Redirect(w, r, authURL, http.StatusSeeOther)
}

// HandleCallback handles the OAuth2 callback.
//
//nolint:cyclop // Complex but necessary for OAuth2 flow
func (am *AuthManager) HandleCallback(w http.ResponseWriter, r *http.Request) {
	// Get state from cookie and query
	stateCookie, err := r.Cookie("oauth_state")
	if err != nil {
		http.Error(w, "Invalid state", http.StatusBadRequest)
		return
	}

	queryState := r.URL.Query().Get("state")
	if queryState != stateCookie.Value {
		http.Error(w, "State mismatch", http.StatusBadRequest)
		return
	}

	providerName := r.URL.Query().Get("provider")
	if providerName == "" {
		http.Error(w, "Provider required", http.StatusBadRequest)
		return
	}

	provider, err := am.GetProvider(providerName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Exchange code for token
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "Authorization code required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	token, err := provider.ExchangeCode(ctx, code)
	if err != nil {
		http.Error(w, fmt.Sprintf("Token exchange failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Get user info
	userInfo, err := provider.GetUserInfo(ctx, token)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get user info: %v", err), http.StatusInternalServerError)
		return
	}

	// Get or create user
	user, err := am.userService.GetOrCreateUser(ctx, userInfo, providerName)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create user: %v", err), http.StatusInternalServerError)
		return
	}

	// Store OAuth token
	oauthToken := &sqlc.OauthToken{
		AccessToken:  token.AccessToken,
		RefreshToken: sql.NullString{String: token.RefreshToken, Valid: token.RefreshToken != ""},
		TokenType:    sql.NullString{String: token.TokenType, Valid: token.TokenType != ""},
		ExpiresAt:    sql.NullTime{Time: token.Expiry, Valid: !token.Expiry.IsZero()},
	}
	_ = am.userService.StoreOAuthToken(ctx, user.ID, providerName, oauthToken)

	// Create session
	sessionToken, err := am.sessionManager.CreateSession(ctx, user.ID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create session: %v", err), http.StatusInternalServerError)
		return
	}

	// Set session cookie
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	isSecure := scheme == "https"
	am.sessionManager.SetSessionCookie(w, sessionToken, isSecure)

	// Clear state cookie
	http.SetCookie(w, &http.Cookie{
		Name:   "oauth_state",
		Value:  "",
		Path:   "/auth/callback",
		MaxAge: -1,
	})

	// Redirect to dashboard
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// HandleLogout handles user logout.
func (am *AuthManager) HandleLogout(w http.ResponseWriter, r *http.Request) {
	token, err := am.sessionManager.GetSessionCookie(r)
	if err == nil {
		_ = am.sessionManager.DestroySession(r.Context(), token)
	}
	am.sessionManager.ClearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// HandleLoginView renders the login page.
func (am *AuthManager) HandleLoginView(w http.ResponseWriter, r *http.Request) {
	// Check if already logged in
	token, err := am.sessionManager.GetSessionCookie(r)
	if err == nil {
		_, err := am.sessionManager.ValidateSession(r.Context(), token)
		if err == nil {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
	}

	// Get available providers
	providers := am.providerFactory.ListProviders()

	// Simple HTML response
	w.Header().Set("Content-Type", "text/html")
	var html strings.Builder
	html.WriteString(`<!DOCTYPE html>
<html>
<head>
	   <title>Login - Support Rota</title>
	   <style>
	       body { font-family: system-ui; max-width: 600px; margin: 50px auto; padding: 20px; }
	       h1 { color: #333; }
	       .provider-list { list-style: none; padding: 0; }
	       .provider-item { margin: 10px 0; }
	       .provider-btn {
	           display: block;
	           width: 100%;
	           padding: 12px;
	           background: #007bff;
	           color: white;
	           text-decoration: none;
	           border-radius: 4px;
	           text-align: center;
	       }
	       .provider-btn:hover { background: #0056b3; }
	   </style>
</head>
<body>
	   <h1>Support Rota Login</h1>
	   <p>Please select your authentication provider:</p>
	   <ul class="provider-list">
`)
	for _, provider := range providers {
		displayName := capitalizeProviderName(provider)
		html.WriteString(fmt.Sprintf(`        <li class="provider-item">
	           <a href="/auth/login/%s" class="provider-btn">Login with %s</a>
	       </li>
`, provider, displayName))
	}
	html.WriteString(`    </ul>
</body>
</html>`)
	_, _ = w.Write([]byte(html.String()))
}

// generateStateToken generates a cryptographically secure state token.
func generateStateToken() (string, error) {
	const tokenSize = 32
	bytes := make([]byte, tokenSize)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(bytes), nil
}

// capitalizeProviderName returns a properly capitalized display name for a provider.
func capitalizeProviderName(provider string) string {
	switch strings.ToLower(provider) {
	case "forgejo":
		return "Forgejo"
	case "gitlab":
		return "GitLab"
	default:
		// Default: capitalize first letter
		if len(provider) == 0 {
			return provider
		}
		return strings.ToUpper(provider[:1]) + provider[1:]
	}
}
