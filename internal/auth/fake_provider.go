package auth

import (
	"context"
	"encoding/base64"
	"fmt"
	"html"
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
	config      ProviderConfig
	resolveUser FakeUserResolver
	listUsers   FakeUserLister
}

const (
	fakeCodeUserMarker        = "-u-"
	fakeAccessTokenUserMarker = "-u-"
)

// DevelopmentLoginUser represents a selectable user in development mode.
type DevelopmentLoginUser struct {
	Key     string
	Name    string
	Email   string
	IsAdmin bool
}

// FakeUserResolver resolves a development login selection into user info.
type FakeUserResolver func(ctx context.Context, key string) (*UserInfo, error)

// FakeUserLister returns selectable users for development login.
type FakeUserLister func(ctx context.Context) ([]DevelopmentLoginUser, error)

// NewFakeProvider creates a new fake OAuth provider.
func NewFakeProvider(config ProviderConfig) *FakeProvider {
	return &FakeProvider{
		config: config,
	}
}

// NewFakeProviderWithUserStore creates a fake provider that can list and resolve users.
func NewFakeProviderWithUserStore(config ProviderConfig, resolver FakeUserResolver, lister FakeUserLister) *FakeProvider {
	return &FakeProvider{
		config:      config,
		resolveUser: resolver,
		listUsers:   lister,
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
	selectedUser, hasSelectedUser := parseSelectedUserFromCode(code)

	// In development mode, we don't actually exchange codes
	// This should never be called if we handle the fake flow correctly
	accessToken := "fake-access-token-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if hasSelectedUser {
		accessToken += fakeAccessTokenUserMarker + encodeSelection(selectedUser)
	}

	return &oauth2.Token{
		AccessToken:  accessToken,
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
	if selectedUser, ok := parseSelectedUserFromAccessToken(token.AccessToken); ok && f.resolveUser != nil {
		user, err := f.resolveUser(ctx, selectedUser)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve selected development user: %w", err)
		}
		return user, nil
	}

	// Return fake user info - in development mode, use a default user when none is selected.
	return fakeUser, nil
}

// ListDevelopmentUsers returns users that can be selected on the development login page.
func (f *FakeProvider) ListDevelopmentUsers(ctx context.Context) ([]DevelopmentLoginUser, error) {
	if f.listUsers == nil {
		return defaultDevelopmentUsers(), nil
	}

	users, err := f.listUsers(ctx)
	if err != nil {
		return nil, err
	}
	if len(users) == 0 {
		return defaultDevelopmentUsers(), nil
	}

	return users, nil
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
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124 false positive: cookie has all required security attributes (HttpOnly, Secure, SameSite); gosec source analysis cannot see through AddCookie call site
		Name:     "oauth_state",
		Value:    randomState,
		Path:     "/auth/callback",
		MaxAge:   StateCookieExpiry,
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	})

	// Generate a fake authorization code.
	selectedUser := strings.TrimSpace(r.URL.Query().Get("user"))
	fakeCode := buildFakeAuthorizationCode(selectedUser)

	// Redirect to callback with fake code and the random state
	callbackURL := "/auth/callback?code=" + url.QueryEscape(fakeCode) + "&state=" + url.QueryEscape(randomState) + "&provider=fake"
	http.Redirect(w, r, callbackURL, http.StatusSeeOther)
}

// GetDevelopmentLoginHTML returns the shared HTML for the development mode login page.
// This eliminates duplication between web and auth packages.
func GetDevelopmentLoginHTML() string {
	return GetDevelopmentLoginHTMLWithUsers(nil)
}

// GetDevelopmentLoginHTMLWithUsers returns the shared HTML for the development mode login page.
func GetDevelopmentLoginHTMLWithUsers(users []DevelopmentLoginUser) string {
	if len(users) == 0 {
		users = defaultDevelopmentUsers()
	}

	var options strings.Builder
	for _, user := range users {
		if strings.TrimSpace(user.Key) == "" {
			continue
		}

		label := strings.TrimSpace(user.Name)
		if label == "" {
			label = user.Email
		}
		if strings.TrimSpace(user.Email) != "" {
			label += " (" + user.Email + ")"
		}
		if user.IsAdmin {
			label += " [Admin]"
		}

		options.WriteString("<option value=\"")
		options.WriteString(html.EscapeString(user.Key))
		options.WriteString("\">")
		options.WriteString(html.EscapeString(label))
		options.WriteString("</option>")
	}

	if options.Len() == 0 {
		defaultUser := defaultDevelopmentUsers()[0]
		options.WriteString("<option value=\"")
		options.WriteString(html.EscapeString(defaultUser.Key))
		options.WriteString("\">")
		options.WriteString(html.EscapeString(defaultUser.Name + " (" + defaultUser.Email + ") [Admin]"))
		options.WriteString("</option>")
	}

	return `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <meta name="description" content="Support Rota — development login.">
    <title>Development Login - MadHatter</title>
    <link rel="stylesheet" href="/static/bulma/bulma.min.css">
    <link rel="stylesheet" href="/static/fontawesome/all.min.css">
    <style>
        body { background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); min-height: 100vh; }
        .hero { background: rgba(255, 255, 255, 0.95); border-radius: 12px; margin: 2rem auto; max-width: 600px; padding: 2rem; box-shadow: 0 20px 60px rgba(0,0,0,0.3); }
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
									<li>- Select any defined active user and click "Login"</li>
									<li>- If no users exist yet, a default "dev@example.com" admin is used</li>
                                    <li>- No real OAuth provider is needed</li>
                                    <li>- Perfect for local development and testing</li>
                                </ul>
                            </div>

                            <div class="content has-text-centered">
                                <form action="/auth/fake/login" method="GET">
									<div class="field">
										<label class="label has-text-left">Login As</label>
										<div class="control">
											<div class="select is-fullwidth">
												<select name="user" required>
													` + options.String() + `
												</select>
											</div>
										</div>
									</div>
                                    <button type="submit" class="button is-primary is-large is-fullwidth">
										<i class="fas fa-sign-in-alt mr-2"></i> Login as Selected User
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

func defaultDevelopmentUsers() []DevelopmentLoginUser {
	return []DevelopmentLoginUser{
		{
			Key:     fakeUser.Email,
			Name:    fakeUser.Name,
			Email:   fakeUser.Email,
			IsAdmin: true,
		},
	}
}

func buildFakeAuthorizationCode(selectedUser string) string {
	baseCode := fmt.Sprintf("fake-code-%d", time.Now().UnixNano())
	if strings.TrimSpace(selectedUser) == "" {
		return baseCode
	}

	return baseCode + fakeCodeUserMarker + encodeSelection(selectedUser)
}

func parseSelectedUserFromCode(code string) (string, bool) {
	index := strings.LastIndex(code, fakeCodeUserMarker)
	if index == -1 {
		return "", false
	}

	encodedUser := code[index+len(fakeCodeUserMarker):]
	selectedUser, err := decodeSelection(encodedUser)
	if err != nil || strings.TrimSpace(selectedUser) == "" {
		return "", false
	}

	return selectedUser, true
}

func parseSelectedUserFromAccessToken(accessToken string) (string, bool) {
	index := strings.LastIndex(accessToken, fakeAccessTokenUserMarker)
	if index == -1 {
		return "", false
	}

	encodedUser := accessToken[index+len(fakeAccessTokenUserMarker):]
	selectedUser, err := decodeSelection(encodedUser)
	if err != nil || strings.TrimSpace(selectedUser) == "" {
		return "", false
	}

	return selectedUser, true
}

func encodeSelection(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeSelection(value string) (string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}

	return string(decoded), nil
}

// FakeCallbackHandler.HandleCallback has been removed in favor of the standard
// AuthManager.HandleCallback, which handles /auth/callback for all providers.
