package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"golang.org/x/oauth2"
)

// GitLabProvider implements OAuth2 authentication for GitLab.
type GitLabProvider struct {
	config ProviderConfig
	oauth  *oauth2.Config
}

// NewGitLabProvider creates a new GitLab OAuth provider.
func NewGitLabProvider(config ProviderConfig) *GitLabProvider {
	return &GitLabProvider{
		config: config,
		oauth: &oauth2.Config{
			ClientID:     config.ClientID,
			ClientSecret: config.ClientSecret,
			RedirectURL:  config.RedirectURL,
			Endpoint: oauth2.Endpoint{
				AuthURL:  config.AuthURL,
				TokenURL: config.TokenURL,
			},
			Scopes: []string{config.Scope},
		},
	}
}

// Name returns the provider name.
func (p *GitLabProvider) Name() string {
	return "gitlab"
}

// GetAuthURL returns the authorization URL.
func (p *GitLabProvider) GetAuthURL(state string) string {
	return p.oauth.AuthCodeURL(state, oauth2.AccessTypeOffline)
}

// ExchangeCode exchanges an authorization code for tokens.
func (p *GitLabProvider) ExchangeCode(ctx context.Context, code string) (*oauth2.Token, error) {
	token, err := p.oauth.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTokenExchange, err)
	}
	return token, nil
}

// GetUserInfo retrieves user information from GitLab.
func (p *GitLabProvider) GetUserInfo(ctx context.Context, token *oauth2.Token) (*UserInfo, error) {
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

	// GitLab user info response structure
	var gitlabUser struct {
		ID        int64  `json:"id"`
		Username  string `json:"username"`
		Name      string `json:"name"`
		Email     string `json:"email"`
		AvatarURL string `json:"avatar_url"`
	}

	if err := json.Unmarshal(body, &gitlabUser); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUserInfo, err)
	}

	return &UserInfo{
		ID:        strconv.FormatInt(gitlabUser.ID, 10),
		Email:     gitlabUser.Email,
		Name:      gitlabUser.Name,
		Username:  gitlabUser.Username,
		AvatarURL: gitlabUser.AvatarURL,
	}, nil
}

// GetOAuthConfig returns the OAuth2 configuration.
func (p *GitLabProvider) GetOAuthConfig() *oauth2.Config {
	return p.oauth
}
