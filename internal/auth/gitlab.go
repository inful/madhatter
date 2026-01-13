//nolint:dupl // Forgejo and GitLab providers share similar OAuth2 structure but are intentionally separate for extensibility
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

const (
	// ProviderNameGitLab is the name of the GitLab provider.
	ProviderNameGitLab = "gitlab"
)

// Name returns the provider name.
func (p *GitLabProvider) Name() string {
	return ProviderNameGitLab
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

	// Check group membership if AllowedGroup is configured
	if p.config.AllowedGroup != "" {
		isMember, err := p.checkGroupMembership(ctx, client, gitlabUser.ID, p.config.AllowedGroup)
		if err != nil {
			return nil, fmt.Errorf("%w: failed to check group membership: %w", ErrUserInfo, err)
		}
		if !isMember {
			return nil, fmt.Errorf("%w: group %q", ErrGroupMembershipDenied, p.config.AllowedGroup)
		}
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

// checkGroupMembership verifies if a user is a member of the specified GitLab group.
// The groupPath should be in the format "group" or "parent/subgroup".
func (p *GitLabProvider) checkGroupMembership(ctx context.Context, client *http.Client, userID int64, groupPath string) (bool, error) {
	// Construct the base URL from the UserInfoURL
	// UserInfoURL is typically "https://gitlab.com/api/v4/user"
	// We need "https://gitlab.com/api/v4"
	baseURL := p.config.UserInfoURL
	if idx := len(baseURL) - len("/user"); idx > 0 && baseURL[idx:] == "/user" {
		baseURL = baseURL[:idx]
	}

	// GitLab API endpoint to get user's groups
	// This returns all groups the user is a member of
	groupsURL := fmt.Sprintf("%s/groups?min_access_level=10", baseURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, groupsURL, nil)
	if err != nil {
		return false, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to fetch groups: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("failed to read response: %w", err)
	}

	// Parse the groups response
	var groups []struct {
		ID       int64  `json:"id"`
		FullPath string `json:"full_path"`
	}

	if err := json.Unmarshal(body, &groups); err != nil {
		return false, fmt.Errorf("failed to parse groups: %w", err)
	}

	// Check if any of the returned groups match the allowed group path
	// Use exact match for security
	for i := range groups {
		if groups[i].FullPath == groupPath {
			return true, nil
		}
	}

	return false, nil
}
