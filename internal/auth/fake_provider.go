package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

const (
	// SessionExpiryDuration is the duration for which sessions remain valid.
	SessionExpiryDuration = 24 * time.Hour
	// SchemeHTTPS is the HTTPS URL scheme.
	SchemeHTTPS = "https"
	// StateCookieExpiry is the duration for which the OAuth state cookie remains valid.
	StateCookieExpiry = 300 // 5 minutes
)

// FakeProvider implements OAuth2 provider for development mode.
// It bypasses real OAuth and creates fake users automatically.
type FakeProvider struct {
	config ProviderConfig
}

// NewFakeProvider creates a new fake OAuth provider.
func NewFakeProvider(config ProviderConfig) *FakeProvider {
	return &FakeProvider{
		config: config,
	}
}

// Name returns the provider name.
func (f *FakeProvider) Name() string {
	return "fake"
}

// GetAuthURL returns a fake authorization URL.
// In development mode, this will be handled by a special callback handler.
func (f *FakeProvider) GetAuthURL(state string) string {
	// Return a URL that will be intercepted by the fake callback handler
	return "/auth/fake/login?state=" + url.QueryEscape(state)
}

// ExchangeCode simulates exchanging an authorization code for tokens.
// In development mode, this returns fake tokens immediately.
func (f *FakeProvider) ExchangeCode(ctx context.Context, code string) (*oauth2.Token, error) {
	// In development mode, we don't actually exchange codes
	// This should never be called if we handle the fake flow correctly
	return &oauth2.Token{
		AccessToken:  "fake-access-token-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		RefreshToken: "fake-refresh-token",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(SessionExpiryDuration),
	}, nil
}

var fakeUser = &UserInfo{
	ID:        "dev-user-1",
	Email:     "dev@example.com",
	Name:      "Development User",
	Username:  "devuser",
	AvatarURL: "",
}

// GetUserInfo returns fake user information.
func (f *FakeProvider) GetUserInfo(ctx context.Context, token *oauth2.Token) (*UserInfo, error) {
	// Return fake user info - in development mode, we'll use a default user
	return fakeUser, nil
}

// GetOAuthConfig returns the OAuth2 configuration.
func (f *FakeProvider) GetOAuthConfig() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     f.config.ClientID,
		ClientSecret: f.config.ClientSecret,
		RedirectURL:  f.config.RedirectURL,
		Endpoint: oauth2.Endpoint{
			AuthURL:  f.config.AuthURL,
			TokenURL: f.config.TokenURL,
		},
		Scopes: strings.Split(f.config.Scope, " "),
	}
}

// FakeCallbackHandler handles the fake OAuth callback flow.
// This bypasses the real OAuth flow and creates a session directly.
type FakeCallbackHandler struct {
	// No fields needed - uses standard callback handler
}

// NewFakeCallbackHandler creates a new fake callback handler.
func NewFakeCallbackHandler() *FakeCallbackHandler {
	return &FakeCallbackHandler{}
}

// HandleLogin handles the fake login flow.
// This creates a fake authorization code and redirects to the callback.
func (h *FakeCallbackHandler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	// Generate a random state for CSRF protection simulation
	// This better simulates real OAuth flow and helps catch state validation issues
	randomState := fmt.Sprintf("dev-state-%d", time.Now().UnixNano())

	// Set state cookie (required for callback validation)
	http.SetCookie(w, &http.Cookie{
		Name:   "oauth_state",
		Value:  randomState,
		Path:   "/auth/callback",
		MaxAge: StateCookieExpiry,
	})

	// Generate a fake authorization code
	fakeCode := fmt.Sprintf("fake-code-%d", time.Now().UnixNano())

	// Redirect to callback with fake code and the random state
	callbackURL := "/auth/callback?code=" + url.QueryEscape(fakeCode) + "&state=" + url.QueryEscape(randomState) + "&provider=fake"
	http.Redirect(w, r, callbackURL, http.StatusSeeOther)
}

// GetDevelopmentLoginHTML returns the shared HTML for the development mode login page.
// This eliminates duplication between web and auth packages.
func GetDevelopmentLoginHTML() string {
	return `<!DOCTYPE html>
<html>
<head>
    <title>Development Login - MadHatter</title>
    <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/bulma@0.9.4/css/bulma.min.css">
    <link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/font-awesome/6.4.0/css/all.min.css">
    <style>
        body { background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); min-height: 100vh; }
        .hero { background: rgba(255, 255, 255, 0.95); border-radius: 12px; margin: 2rem auto; max-width: 600px; box-shadow: 0 20px 60px rgba(0,0,0,0.3); }
        .card { border-radius: 8px; box-shadow: 0 4px 12px rgba(0,0,0,0.1); }
        .dev-warning { background: #fff3cd; border: 2px solid #ffc107; padding: 1rem; border-radius: 8px; margin-bottom: 1.5rem; }
        .dev-info { background: #e7f3ff; padding: 1rem; border-radius: 8px; margin-bottom: 1.5rem; }
        .dev-warning strong { color: #856404; }
    </style>
</head>
<body>
    <section class="section">
        <div class="hero">
            <div class="hero-body">
                <div class="container">
                    <h1 class="title is-2 has-text-centered has-text-primary">
                        <i class="fas fa-flask"></i> Development Mode
                    </h1>
                    <p class="subtitle has-text-centered">Fake OAuth Authentication</p>

                    <div class="card">
                        <div class="card-content">
                            <div class="dev-warning">
                                <p class="has-text-centered has-text-weight-semibold mb-2">
                                    <i class="fas fa-exclamation-triangle"></i> DEVELOPMENT MODE ENABLED
                                </p>
                                <p class="is-size-7 has-text-centered">
                                    You are running in development mode with fake OAuth authentication.<br>
                                    <strong>DO NOT use this in production!</strong>
                                </p>
                            </div>

                            <div class="dev-info">
                                <p class="has-text-weight-semibold mb-2"><i class="fas fa-info-circle"></i> How it works:</p>
                                <ul class="is-size-7">
                                    <li>- Clicking "Login" creates a fake user "dev@example.com"</li>
                                    <li>- The user automatically becomes an admin</li>
                                    <li>- No real OAuth provider is needed</li>
                                    <li>- Perfect for local development and testing</li>
                                </ul>
                            </div>

                            <div class="content has-text-centered">
                                <form action="/auth/fake/login" method="GET">
                                    <button type="submit" class="button is-primary is-large is-fullwidth">
                                        <i class="fas fa-sign-in-alt mr-2"></i> Login as Development User
                                    </button>
                                </form>
                            </div>
                        </div>
                    </div>

                    <div class="has-text-centered">
                        <a href="/" class="button is-light is-small">
                            <i class="fas fa-arrow-left mr-1"></i> Back to Home
                        </a>
                    </div>
                </div>
            </div>
        </div>
    </section>
</body>
</html>`
}

// FakeCallbackHandler.HandleCallback has been removed in favor of the standard
// AuthManager.HandleCallback, which handles /auth/callback for all providers.
