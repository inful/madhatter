package notify

import (
	"os"
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
	assert.Empty(t, cfg.EnabledChannels, "channel list defaults to empty when email is disabled")
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

// TestGetEnvBool locks in the contract of the helper that will be
// deduped with wfh/service.go::parseBoolEnv. Same contract as its
// sibling: empty/unset/garbage → default; otherwise parsed.
func TestGetEnvBool(t *testing.T) {
	t.Run("UnsetReturnsDefault", func(t *testing.T) {
		key := "NOTIFY_TEST_UNSET_BOOL"
		require.NoError(t, os.Unsetenv(key))
		assert.True(t, getEnvBool(key, true))
		assert.False(t, getEnvBool(key, false))
	})

	t.Run("EmptyReturnsDefault", func(t *testing.T) {
		key := "NOTIFY_TEST_EMPTY_BOOL"
		t.Setenv(key, "")
		assert.True(t, getEnvBool(key, true))
		assert.False(t, getEnvBool(key, false))
	})

	t.Run("ParsesTruthyValues", func(t *testing.T) {
		key := "NOTIFY_TEST_TRUTHY_BOOL"
		for _, v := range []string{"true", "TRUE", "True", "1", "t", "T"} {
			t.Setenv(key, v)
			assert.True(t, getEnvBool(key, false), "value %q must parse true", v)
		}
	})

	t.Run("ParsesFalsyValues", func(t *testing.T) {
		key := "NOTIFY_TEST_FALSY_BOOL"
		for _, v := range []string{"false", "FALSE", "False", "0", "f", "F"} {
			t.Setenv(key, v)
			assert.False(t, getEnvBool(key, true), "value %q must parse false", v)
		}
	})

	t.Run("GarbageFallsBackToDefault", func(t *testing.T) {
		key := "NOTIFY_TEST_GARBAGE_BOOL"
		t.Setenv(key, "not-a-bool")
		assert.True(t, getEnvBool(key, true), "garbage must fall back to default true")
		assert.False(t, getEnvBool(key, false), "garbage must fall back to default false")
	})
}

// TestGetEnvInt covers the int helper used by LoadConfigFromEnv.
func TestGetEnvInt(t *testing.T) {
	t.Run("UnsetReturnsDefault", func(t *testing.T) {
		key := "NOTIFY_TEST_UNSET_INT"
		require.NoError(t, os.Unsetenv(key))
		assert.Equal(t, 42, getEnvInt(key, 42))
	})

	t.Run("ParsesValue", func(t *testing.T) {
		key := "NOTIFY_TEST_PARSE_INT"
		t.Setenv(key, "7")
		assert.Equal(t, 7, getEnvInt(key, 0))
	})

	t.Run("GarbageFallsBackToDefault", func(t *testing.T) {
		key := "NOTIFY_TEST_GARBAGE_INT"
		t.Setenv(key, "not-an-int")
		assert.Equal(t, 99, getEnvInt(key, 99))
	})
}

// TestGetEnvDuration covers the duration helper.
func TestGetEnvDuration(t *testing.T) {
	t.Run("UnsetReturnsDefault", func(t *testing.T) {
		key := "NOTIFY_TEST_UNSET_DUR"
		require.NoError(t, os.Unsetenv(key))
		assert.Equal(t, 30*time.Second, getEnvDuration(key, 30*time.Second))
	})

	t.Run("ParsesValue", func(t *testing.T) {
		key := "NOTIFY_TEST_PARSE_DUR"
		t.Setenv(key, "5m")
		assert.Equal(t, 5*time.Minute, getEnvDuration(key, time.Second))
	})

	t.Run("GarbageFallsBackToDefault", func(t *testing.T) {
		key := "NOTIFY_TEST_GARBAGE_DUR"
		t.Setenv(key, "nope")
		assert.Equal(t, 7*time.Second, getEnvDuration(key, 7*time.Second))
	})
}

// TestGetEnvOrDefault covers the string helper.
func TestGetEnvOrDefault(t *testing.T) {
	t.Run("UnsetReturnsDefault", func(t *testing.T) {
		key := "NOTIFY_TEST_UNSET_STR"
		require.NoError(t, os.Unsetenv(key))
		assert.Equal(t, "fallback", getEnvOrDefault(key, "fallback"))
	})

	t.Run("EmptyReturnsDefault", func(t *testing.T) {
		key := "NOTIFY_TEST_EMPTY_STR"
		t.Setenv(key, "")
		assert.Equal(t, "fallback", getEnvOrDefault(key, "fallback"))
	})

	t.Run("ParsesValue", func(t *testing.T) {
		key := "NOTIFY_TEST_PARSE_STR"
		t.Setenv(key, "value")
		assert.Equal(t, "value", getEnvOrDefault(key, "fallback"))
	})
}
