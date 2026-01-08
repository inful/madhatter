package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultSessionDuration = 24
)

// AuthConfig represents the authentication configuration.
type AuthConfig struct {
	Providers map[string]ProviderConfig `yaml:"providers"`
	Sessions  SessionConfig             `yaml:"sessions"`
}

// SessionConfig represents session-related settings.
type SessionConfig struct {
	DurationHours int    `yaml:"duration_hours"`
	CookieName    string `yaml:"cookie_name"`
	SecretKey     string `yaml:"secret_key"`
}

// ProviderConfig represents OAuth provider configuration.
type ProviderConfig struct {
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	RedirectURL  string `yaml:"redirect_url"`
	AuthURL      string `yaml:"auth_url"`
	TokenURL     string `yaml:"token_url"`
	UserInfoURL  string `yaml:"user_info_url"`
	Scope        string `yaml:"scope"`
}

// LoadConfig loads authentication configuration from a YAML file.
// The path must resolve to a location within the working directory
// to prevent directory traversal attacks.
func LoadConfig(path string) (*AuthConfig, error) {
	// Validate the path and get the cleaned version
	cleanPath, err := validateConfigPath(path)
	if err != nil {
		return nil, err
	}

	// #nosec G304 - The path is validated by validateConfigPath to prevent directory traversal
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config AuthConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	return &config, nil
}

// validateConfigPath ensures the config file path is within allowed directories
// and doesn't escape via directory traversal. Returns the cleaned path if valid.
func validateConfigPath(path string) (string, error) {
	// Clean the path first
	cleanPath := filepath.Clean(path)

	// Get absolute paths for comparison
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve config path: %w", err)
	}

	// Get current working directory as the base allowed directory
	workDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get working directory: %w", err)
	}

	// Check if the absolute path is within the working directory
	relPath, err := filepath.Rel(workDir, absPath)
	if err != nil {
		return "", fmt.Errorf("failed to determine relative path: %w", err)
	}

	// filepath.Rel returns a path starting with ".." if the target is outside the base
	// Check for ".." at the start followed by a separator or as the entire path
	// Handle both OS-specific separator and forward slash (which can work on Windows)
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) || strings.HasPrefix(relPath, "../") {
		return "", fmt.Errorf("config path %q is outside the allowed directory", path)
	}

	return cleanPath, nil
}

// LoadConfigFromEnv loads configuration from environment variables.
// This is useful for containerized deployments.
func LoadConfigFromEnv() *AuthConfig {
	config := &AuthConfig{
		Providers: make(map[string]ProviderConfig),
		Sessions: SessionConfig{
			DurationHours: defaultSessionDuration,
			CookieName:    "session_token",
			SecretKey:     os.Getenv("SESSION_SECRET"),
		},
	}

	// Load Forgejo config from env
	if clientID := os.Getenv("FORGEJO_CLIENT_ID"); clientID != "" {
		config.Providers["forgejo"] = ProviderConfig{
			ClientID:     clientID,
			ClientSecret: os.Getenv("FORGEJO_CLIENT_SECRET"),
			RedirectURL:  os.Getenv("FORGEJO_REDIRECT_URL"),
			AuthURL:      getEnvOrDefault("FORGEJO_AUTH_URL", "/login/oauth/authorize"),
			TokenURL:     getEnvOrDefault("FORGEJO_TOKEN_URL", "/login/oauth/access_token"),
			UserInfoURL:  getEnvOrDefault("FORGEJO_USERINFO_URL", "/api/v1/user"),
			Scope:        getEnvOrDefault("FORGEJO_SCOPE", "read:user"),
		}
	}

	// Load GitLab config from env
	if clientID := os.Getenv("GITLAB_CLIENT_ID"); clientID != "" {
		config.Providers["gitlab"] = ProviderConfig{
			ClientID:     clientID,
			ClientSecret: os.Getenv("GITLAB_CLIENT_SECRET"),
			RedirectURL:  os.Getenv("GITLAB_REDIRECT_URL"),
			AuthURL:      getEnvOrDefault("GITLAB_AUTH_URL", "https://gitlab.com/oauth/authorize"),
			TokenURL:     getEnvOrDefault("GITLAB_TOKEN_URL", "https://gitlab.com/oauth/token"),
			UserInfoURL:  getEnvOrDefault("GITLAB_USERINFO_URL", "https://gitlab.com/api/v4/user"),
			Scope:        getEnvOrDefault("GITLAB_SCOPE", "read_user"),
		}
	}

	return config
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// Validate checks if the configuration is valid.
func (c *AuthConfig) Validate() error {
	if len(c.Providers) == 0 {
		return errors.New(
			"no OAuth providers configured. Please set at least one of the following environment variables:\n\n" +
				"For Forgejo:\n" +
				"  - FORGEJO_CLIENT_ID\n" +
				"  - FORGEJO_CLIENT_SECRET\n" +
				"  - FORGEJO_REDIRECT_URL\n\n" +
				"For GitLab:\n" +
				"  - GITLAB_CLIENT_ID\n" +
				"  - GITLAB_CLIENT_SECRET\n" +
				"  - GITLAB_REDIRECT_URL\n\n" +
				"Optional environment variables:\n" +
				"  - FORGEJO_AUTH_URL (default: /login/oauth/authorize)\n" +
				"  - FORGEJO_TOKEN_URL (default: /login/oauth/access_token)\n" +
				"  - FORGEJO_USERINFO_URL (default: /api/v1/user)\n" +
				"  - FORGEJO_SCOPE (default: read:user)\n" +
				"  - GITLAB_AUTH_URL (default: https://gitlab.com/oauth/authorize)\n" +
				"  - GITLAB_TOKEN_URL (default: https://gitlab.com/oauth/token)\n" +
				"  - GITLAB_USERINFO_URL (default: https://gitlab.com/api/v4/user)\n" +
				"  - GITLAB_SCOPE (default: read_user)\n\n" +
				"See AUTH_SETUP.md for detailed configuration instructions",
		)
	}

	for name, provider := range c.Providers {
		if provider.ClientID == "" {
			return fmt.Errorf("provider %s: client_id is required", name)
		}
		if provider.ClientSecret == "" {
			return fmt.Errorf("provider %s: client_secret is required", name)
		}
		if provider.RedirectURL == "" {
			return fmt.Errorf("provider %s: redirect_url is required", name)
		}
	}

	return nil
}

// GenerateExampleConfig generates an example configuration file.
func GenerateExampleConfig() string {
	return `# Authentication Configuration
# Copy this to config/auth.yaml and fill in your values

providers:
  forgejo:
    client_id: "your-forgejo-client-id"
    client_secret: "your-forgejo-client-secret"
    redirect_url: "http://localhost:8080/auth/callback?provider=forgejo"
    auth_url: "https://git.example.com/login/oauth/authorize"
    token_url: "https://git.example.com/login/oauth/access_token"
    user_info_url: "https://git.example.com/api/v1/user"
    scope: "read:user"

  gitlab:
    client_id: "your-gitlab-client-id"
    client_secret: "your-gitlab-client-secret"
    redirect_url: "http://localhost:8080/auth/callback?provider=gitlab"
    auth_url: "https://gitlab.com/oauth/authorize"
    token_url: "https://gitlab.com/oauth/token"
    user_info_url: "https://gitlab.com/api/v4/user"
    scope: "read_user"

sessions:
  duration_hours: 24
  cookie_name: "session_token"
  secret_key: "generate-a-random-secret-key-here"
`
}
