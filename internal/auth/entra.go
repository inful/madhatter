package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/oauth2"
)

// EntraProvider implements OAuth2 authentication for Microsoft Entra ID.
// This uses the v2.0 endpoints and Graph /me for user info.
//
// Expected env config:
// - ENTRA_TENANT_ID (single-tenant)
// - ENTRA_CLIENT_ID
// - ENTRA_CLIENT_SECRET
// - ENTRA_REDIRECT_URL
// - optional ENTRA_SCOPE (defaults include openid/offline_access/User.Read)
//
// IMPORTANT: We do not validate id_tokens here; this provider follows the existing pattern
// used by Forgejo/GitLab and relies on calling Graph /me with the access token.
// A future hardening step is to validate OIDC id_tokens.
type EntraProvider struct {
	config ProviderConfig
	oauth  *oauth2.Config
}

const (
	ProviderNameEntra = "entra"
)

func NewEntraProvider(config ProviderConfig) *EntraProvider {
	return &EntraProvider{
		config: config,
		oauth: &oauth2.Config{
			ClientID:     config.ClientID,
			ClientSecret: config.ClientSecret,
			RedirectURL:  config.RedirectURL,
			Endpoint: oauth2.Endpoint{
				AuthURL:  config.AuthURL,
				TokenURL: config.TokenURL,
			},
			Scopes: parseScopes(config.Scope),
		},
	}
}

func (p *EntraProvider) Name() string {
	return ProviderNameEntra
}

func (p *EntraProvider) GetAuthURL(state string) string {
	return p.oauth.AuthCodeURL(state, oauth2.AccessTypeOffline)
}

func (p *EntraProvider) ExchangeCode(ctx context.Context, code string) (*oauth2.Token, error) {
	token, err := p.oauth.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTokenExchange, err)
	}
	return token, nil
}

func (p *EntraProvider) GetUserInfo(ctx context.Context, token *oauth2.Token) (*UserInfo, error) {
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

	var me struct {
		ID                string `json:"id"`
		DisplayName       string `json:"displayName"`
		Mail              string `json:"mail"`
		UserPrincipalName string `json:"userPrincipalName"`
	}
	if err := json.Unmarshal(body, &me); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUserInfo, err)
	}

	email := strings.TrimSpace(me.Mail)
	if email == "" {
		email = strings.TrimSpace(me.UserPrincipalName)
	}

	return &UserInfo{
		ID:       me.ID,
		Email:    email,
		Name:     me.DisplayName,
		Username: me.UserPrincipalName,
	}, nil
}

func (p *EntraProvider) GetOAuthConfig() *oauth2.Config {
	return p.oauth
}
