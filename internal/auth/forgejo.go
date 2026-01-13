//nolint:dupl // Forgejo and GitLab providers share similar OAuth2 structure but are intentionally separate for extensibility
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/oauth2"
)

// ForgejoProvider implements OAuth2 authentication for Forgejo.
type ForgejoProvider struct {
	config ProviderConfig
	oauth  *oauth2.Config
}

// NewForgejoProvider creates a new Forgejo OAuth provider.
func NewForgejoProvider(config ProviderConfig) *ForgejoProvider {
	// Split space-separated scopes into array
	scopes := strings.Fields(config.Scope)
	return &ForgejoProvider{
		config: config,
		oauth: &oauth2.Config{
			ClientID:     config.ClientID,
			ClientSecret: config.ClientSecret,
			RedirectURL:  config.RedirectURL,
			Endpoint: oauth2.Endpoint{
				AuthURL:  config.AuthURL,
				TokenURL: config.TokenURL,
			},
			Scopes: scopes,
		},
	}
}

const (
	// ProviderNameForgejo is the name of the Forgejo provider.
	ProviderNameForgejo = "forgejo"
)

// Name returns the provider name.
func (p *ForgejoProvider) Name() string {
	return ProviderNameForgejo
}

// GetAuthURL returns the authorization URL.
func (p *ForgejoProvider) GetAuthURL(state string) string {
	return p.oauth.AuthCodeURL(state, oauth2.AccessTypeOffline)
}

// ExchangeCode exchanges an authorization code for tokens.
func (p *ForgejoProvider) ExchangeCode(ctx context.Context, code string) (*oauth2.Token, error) {
	token, err := p.oauth.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTokenExchange, err)
	}
	return token, nil
}

// GetUserInfo retrieves user information from Forgejo.
func (p *ForgejoProvider) GetUserInfo(ctx context.Context, token *oauth2.Token) (*UserInfo, error) {
	client := p.oauth.Client(ctx, token)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.config.UserInfoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUserInfo, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUserInfo, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d", ErrUserInfo, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUserInfo, err)
	}

	// Forgejo user info response structure
	var forgejoUser struct {
		ID       int64  `json:"id"`
		Login    string `json:"login"`
		FullName string `json:"full_name"`
		Email    string `json:"email"`
		Avatar   string `json:"avatar_url"`
	}

	if err := json.Unmarshal(body, &forgejoUser); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUserInfo, err)
	}

	return &UserInfo{
		ID:        strconv.FormatInt(forgejoUser.ID, 10),
		Email:     forgejoUser.Email,
		Name:      forgejoUser.FullName,
		Username:  forgejoUser.Login,
		AvatarURL: forgejoUser.Avatar,
	}, nil
}

// GetOAuthConfig returns the OAuth2 configuration.
func (p *ForgejoProvider) GetOAuthConfig() *oauth2.Config {
	return p.oauth
}
