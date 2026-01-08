package auth

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateConfigPath(t *testing.T) {
	// Get current working directory for test setup
	workDir, err := os.Getwd()
	require.NoError(t, err)

	tests := []struct {
		name        string
		path        string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "valid relative path in current dir",
			path:        "config.yaml",
			expectError: false,
		},
		{
			name:        "valid relative path in subdirectory",
			path:        "config/auth.yaml",
			expectError: false,
		},
		{
			name:        "valid path with ./ prefix",
			path:        "./config/auth.yaml",
			expectError: false,
		},
		{
			name:        "valid path with file starting with ..",
			path:        "..hidden-config.yaml",
			expectError: false,
		},
		{
			name:        "valid absolute path within workdir",
			path:        filepath.Join(workDir, "config", "auth.yaml"),
			expectError: false,
		},
		{
			name:        "invalid path with parent directory traversal",
			path:        "../config.yaml",
			expectError: true,
			errorMsg:    "outside the allowed directory",
		},
		{
			name:        "invalid path with multiple parent traversals",
			path:        "../../etc/passwd",
			expectError: true,
			errorMsg:    "outside the allowed directory",
		},
		{
			name:        "invalid path with traversal in middle",
			path:        "config/../../etc/passwd",
			expectError: true,
			errorMsg:    "outside the allowed directory",
		},
		{
			name:        "invalid absolute path outside workdir",
			path:        "/etc/passwd",
			expectError: true,
			errorMsg:    "outside the allowed directory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateConfigPath(tt.path)
			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestLoadConfig_PathValidation(t *testing.T) {
	// Create a temporary config file for testing
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "test-auth.yaml")
	configContent := `providers:
  test:
    client_id: "test-id"
    client_secret: "test-secret"
    redirect_url: "http://localhost/callback"
    auth_url: "http://localhost/auth"
    token_url: "http://localhost/token"
    user_info_url: "http://localhost/user"
    scope: "read:user"
sessions:
  duration_hours: 24
  cookie_name: "test_session"
  secret_key: "test-secret-key"
`
	err := os.WriteFile(configPath, []byte(configContent), 0600)
	require.NoError(t, err)

	// Change to temp directory for the test
	originalDir, err := os.Getwd()
	require.NoError(t, err)
	defer func() {
		err := os.Chdir(originalDir)
		require.NoError(t, err)
	}()

	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	t.Run("loads valid config within working directory", func(t *testing.T) {
		config, err := LoadConfig("test-auth.yaml")
		require.NoError(t, err)
		assert.NotNil(t, config)
		assert.Equal(t, "test-id", config.Providers["test"].ClientID)
	})

	t.Run("rejects config outside working directory", func(t *testing.T) {
		config, err := LoadConfig("../test-auth.yaml")
		assert.Error(t, err)
		assert.Nil(t, config)
		assert.Contains(t, err.Error(), "outside the allowed directory")
	})

	t.Run("rejects absolute path outside working directory", func(t *testing.T) {
		config, err := LoadConfig("/etc/passwd")
		assert.Error(t, err)
		assert.Nil(t, config)
		assert.Contains(t, err.Error(), "outside the allowed directory")
	})
}

func TestLoadConfig_Validation(t *testing.T) {
	// Create a temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "auth.yaml")

	// Change to temp directory for the test
	originalDir, err := os.Getwd()
	require.NoError(t, err)
	defer func() {
		err := os.Chdir(originalDir)
		require.NoError(t, err)
	}()

	err = os.Chdir(tmpDir)
	require.NoError(t, err)

	t.Run("valid config", func(t *testing.T) {
		configContent := `providers:
  forgejo:
    client_id: "forgejo-id"
    client_secret: "forgejo-secret"
    redirect_url: "http://localhost/callback"
    auth_url: "http://localhost/auth"
    token_url: "http://localhost/token"
    user_info_url: "http://localhost/user"
    scope: "read:user"
sessions:
  duration_hours: 24
  cookie_name: "session"
  secret_key: "secret-key"
`
		err := os.WriteFile(configPath, []byte(configContent), 0600)
		require.NoError(t, err)

		config, err := LoadConfig("auth.yaml")
		require.NoError(t, err)
		assert.NotNil(t, config)
		assert.Len(t, config.Providers, 1)
		assert.Equal(t, "forgejo-id", config.Providers["forgejo"].ClientID)
		assert.Equal(t, 24, config.Sessions.DurationHours)
	})

	t.Run("invalid yaml", func(t *testing.T) {
		// Missing closing quote for client_secret value
		invalidContent := `providers:
  forgejo:
    client_id: "id"
    client_secret: "secret
`
		err := os.WriteFile(configPath, []byte(invalidContent), 0600)
		require.NoError(t, err)

		config, err := LoadConfig("auth.yaml")
		assert.Error(t, err)
		assert.Nil(t, config)
		assert.Contains(t, err.Error(), "failed to parse config")
	})

	t.Run("nonexistent file", func(t *testing.T) {
		config, err := LoadConfig("nonexistent.yaml")
		assert.Error(t, err)
		assert.Nil(t, config)
		assert.Contains(t, err.Error(), "failed to read config file")
	})
}
