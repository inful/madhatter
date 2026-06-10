package notify

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfigFromEnv_Defaults(t *testing.T) {
	t.Setenv("NOTIFY_BASE_URL", "")
	t.Setenv("NOTIFY_CHANNELS", "")
	t.Setenv("NOTIFY_EMAIL_ENABLED", "")
	t.Setenv("NOTIFY_SMTP_HOST", "")
	t.Setenv("NOTIFY_SMTP_FROM", "")
	t.Setenv("NOTIFY_OUTBOX_POLL_INTERVAL", "")
	t.Setenv("NOTIFY_OUTBOX_MAX_ATTEMPTS", "")
	t.Setenv("NOTIFY_OUTBOX_BACKOFF_BASE", "")

	cfg := LoadConfigFromEnv()

	assert.Equal(t, "http://localhost:8080", cfg.BaseURL)
	assert.Equal(t, []string{ChannelEmail}, cfg.EnabledChannels)
	assert.False(t, cfg.Email.Enabled)
	assert.Equal(t, "MadHatter Rota <noreply@example.com>", cfg.Email.From)
	assert.Equal(t, 30*time.Second, cfg.Outbox.PollInterval)
	assert.Equal(t, 5, cfg.Outbox.MaxAttempts)
	assert.Equal(t, 30*time.Second, cfg.Outbox.BackoffBase)
}

func TestLoadConfigFromEnv_Overrides(t *testing.T) {
	t.Setenv("NOTIFY_BASE_URL", "https://rota.example.com")
	t.Setenv("NOTIFY_CHANNELS", "email,slack")
	t.Setenv("NOTIFY_EMAIL_ENABLED", "true")
	t.Setenv("NOTIFY_SMTP_HOST", "smtp.example.com:587")
	t.Setenv("NOTIFY_SMTP_FROM", "Rota <noreply@example.com>")
	t.Setenv("NOTIFY_OUTBOX_POLL_INTERVAL", "5s")
	t.Setenv("NOTIFY_OUTBOX_MAX_ATTEMPTS", "10")
	t.Setenv("NOTIFY_OUTBOX_BACKOFF_BASE", "1m")

	cfg := LoadConfigFromEnv()

	assert.Equal(t, "https://rota.example.com", cfg.BaseURL)
	assert.Equal(t, []string{ChannelEmail, ChannelSlack}, cfg.EnabledChannels)
	assert.True(t, cfg.Email.Enabled)
	assert.Equal(t, "smtp.example.com:587", cfg.Email.Host)
	assert.Equal(t, "Rota <noreply@example.com>", cfg.Email.From)
	assert.Equal(t, 5*time.Second, cfg.Outbox.PollInterval)
	assert.Equal(t, 10, cfg.Outbox.MaxAttempts)
	assert.Equal(t, time.Minute, cfg.Outbox.BackoffBase)
}

func TestConfig_Validate_NoChannels(t *testing.T) {
	cfg := Config{
		Outbox: OutboxConfig{
			PollInterval: time.Second,
			MaxAttempts:  1,
			BackoffBase:  time.Second,
		},
	} // no channels, valid
	assert.NoError(t, cfg.Validate())
}

func TestConfig_Validate_EmailEnabled_MissingHost(t *testing.T) {
	cfg := Config{
		EnabledChannels: []string{ChannelEmail},
		Email:           EmailConfig{Enabled: true, From: "x@y.z"},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "NOTIFY_SMTP_HOST")
}

func TestConfig_Validate_EmailEnabled_MissingFrom(t *testing.T) {
	cfg := Config{
		EnabledChannels: []string{ChannelEmail},
		Email:           EmailConfig{Enabled: true, Host: "smtp.example.com:25"},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "NOTIFY_SMTP_FROM")
}

func TestConfig_Validate_EmailListedButFlagOff(t *testing.T) {
	cfg := Config{
		EnabledChannels: []string{ChannelEmail},
		Email:           EmailConfig{Enabled: false, Host: "smtp.example.com:25", From: "x@y.z"},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "NOTIFY_EMAIL_ENABLED")
}

func TestConfig_Validate_UnknownChannel(t *testing.T) {
	cfg := Config{
		EnabledChannels: []string{"telegram"},
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "telegram")
}

func TestConfig_Validate_BadOutbox(t *testing.T) {
	cfg := Config{Outbox: OutboxConfig{PollInterval: 0, MaxAttempts: 1, BackoffBase: time.Second}}
	err := cfg.Validate()
	require.Error(t, err)

	cfg = Config{Outbox: OutboxConfig{PollInterval: time.Second, MaxAttempts: 0, BackoffBase: time.Second}}
	err = cfg.Validate()
	require.Error(t, err)
}

func TestSplitCSV(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{ChannelEmail, []string{ChannelEmail}},
		{ChannelEmail + "," + ChannelSlack, []string{ChannelEmail, ChannelSlack}},
		{"  " + ChannelEmail + "  ,  " + ChannelSlack + "  ", []string{ChannelEmail, ChannelSlack}},
		{ChannelEmail + ",," + ChannelSlack, []string{ChannelEmail, ChannelSlack}},
	}
	for _, tc := range cases {
		got := splitCSV(tc.in)
		assert.Equal(t, tc.want, got, "splitCSV(%q)", tc.in)
	}
}
