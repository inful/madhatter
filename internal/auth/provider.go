package auth

import (
	"context"

	"golang.org/x/oauth2"
)

// Provider represents an OAuth2 provider interface.
// This allows easy addition of new providers.
type Provider interface {
	// Name returns the provider name (e.g., "forgejo", "gitlab")
	Name() string

	// GetAuthURL returns the OAuth2 authorization URL
	GetAuthURL(state string) string

	// ExchangeCode exchanges an authorization code for tokens
	ExchangeCode(ctx context.Context, code string) (*oauth2.Token, error)

	// GetUserInfo retrieves user information from the provider
	GetUserInfo(ctx context.Context, token *oauth2.Token) (*UserInfo, error)

	// GetOAuthConfig returns the OAuth2 configuration
	GetOAuthConfig() *oauth2.Config
}

// UserInfo represents user information from an OAuth provider.
type UserInfo struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url,omitempty"`
	Username  string `json:"username,omitempty"`
}

// ProviderFactory creates provider instances.
type ProviderFactory struct {
	configs map[string]ProviderConfig
}

// NewProviderFactory creates a new factory with provider configurations.
func NewProviderFactory(configs map[string]ProviderConfig) *ProviderFactory {
	return &ProviderFactory{
		configs: configs,
	}
}

// Create creates a provider by name.
func (f *ProviderFactory) Create(name string) (Provider, error) {
	config, exists := f.configs[name]
	if !exists {
		return nil, ErrProviderNotFound
	}

	switch name {
	case "forgejo":
		return NewForgejoProvider(config), nil
	case "gitlab":
		return NewGitLabProvider(config), nil
	case "entra":
		return NewEntraProvider(config), nil
	default:
		return nil, ErrProviderNotFound
	}
}

// ListProviders returns all configured provider names.
func (f *ProviderFactory) ListProviders() []string {
	names := make([]string, 0, len(f.configs))
	for name := range f.configs {
		names = append(names, name)
	}
	return names
}
