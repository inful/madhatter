package notify

import (
	"errors"
	"os"
	"time"

	"github.com/inful/madhatter/internal/envutil"
)

// Config is the runtime configuration for the notification subsystem.
// All values are loaded from environment variables by LoadConfigFromEnv.
//
// The struct is intentionally flat — there is one sub-section per channel
// in v1 (Email). When a second channel is added, prefer a separate struct
// nested under the channel name rather than growing this one.
type Config struct {
	// BaseURL is used in templates for "view in dashboard" links.
	BaseURL string

	// PublicBaseURL is the externally-visible origin used for
	// absolute URLs in emails (one-click unsubscribe links). Falls
	// back to BaseURL when unset.
	PublicBaseURL string

	// EnabledChannels lists the channel names whose outbox rows should
	// be written. In v1, the only valid value is "email". Unknown
	// channel names are ignored by the notifier.
	EnabledChannels []string

	// Email holds SMTP configuration. Ignored unless "email" is in
	// EnabledChannels.
	Email EmailConfig

	// Outbox controls the worker's polling cadence and retry policy.
	Outbox OutboxConfig
}

// EmailConfig is the SMTP configuration for the email channel. Empty
// fields fall through to the underlying mail library's defaults where
// possible; missing required values cause New to return an error.
type EmailConfig struct {
	Enabled  bool
	Host     string // "smtp.example.com:587"
	User     string // empty for anonymous relay
	Password string
	From     string // "MadHatter Rota <noreply@example.com>"
	Identity string // smtp.PlainAuth identity; empty defaults to User
}

// OutboxConfig controls the outbox worker.
type OutboxConfig struct {
	PollInterval time.Duration // default 30s
	MaxAttempts  int           // default 5
	BackoffBase  time.Duration // default 30s, cap 1h
}

// Default values for the outbox worker. Exposed as named constants so
// the mnd linter rule doesn't fire on literal durations.
const (
	defaultOutboxPollInterval = 30 * time.Second
	defaultOutboxMaxAttempts  = 5
	defaultOutboxBackoffBase  = 30 * time.Second
)

// LoadConfigFromEnv reads the notify configuration from the process
// environment. It never returns an error for missing values; defaults
// are applied. New() validates the loaded config and returns an error
// for values that cannot be defaulted (e.g. missing SMTP host when
// email is enabled).
func LoadConfigFromEnv() Config {
	// Default channel list depends on whether email is enabled: when
	// no env vars are set, the default is to leave the channel list
	// empty (i.e. log-only mode) and let the server register a
	// LogChannel so handlers don't fail. Operators who want email
	// must set NOTIFY_CHANNELS=email AND NOTIFY_EMAIL_ENABLED=true.
	channels := os.Getenv("NOTIFY_CHANNELS")
	emailEnabled := envutil.Bool("NOTIFY_EMAIL_ENABLED", false)
	var enabledChannels []string
	if channels != "" {
		enabledChannels = splitCSV(channels)
	} else if emailEnabled {
		enabledChannels = []string{ChannelEmail}
	}
	baseURL := envutil.String("NOTIFY_BASE_URL", "http://localhost:8080")
	// PublicBaseURL is the externally-visible origin used for
	// absolute URLs in emails (one-click unsubscribe links). In
	// production this should be the public HTTPS host; in dev
	// we fall back to BaseURL. The fallback is intentional so
	// --development mode produces working links without extra
	// config.
	publicBaseURL := os.Getenv("NOTIFY_PUBLIC_BASE_URL")
	if publicBaseURL == "" {
		publicBaseURL = baseURL
	}
	return Config{
		BaseURL:         baseURL,
		PublicBaseURL:   publicBaseURL,
		EnabledChannels: enabledChannels,
		Email: EmailConfig{
			Enabled:  emailEnabled,
			Host:     os.Getenv("NOTIFY_SMTP_HOST"),
			User:     os.Getenv("NOTIFY_SMTP_USER"),
			Password: os.Getenv("NOTIFY_SMTP_PASSWORD"),
			From:     envutil.String("NOTIFY_SMTP_FROM", "MadHatter Rota <noreply@example.com>"),
			Identity: os.Getenv("NOTIFY_SMTP_IDENTITY"),
		},
		Outbox: OutboxConfig{
			PollInterval: envutil.Duration("NOTIFY_OUTBOX_POLL_INTERVAL", defaultOutboxPollInterval),
			MaxAttempts:  envutil.Int("NOTIFY_OUTBOX_MAX_ATTEMPTS", defaultOutboxMaxAttempts),
			BackoffBase:  envutil.Duration("NOTIFY_OUTBOX_BACKOFF_BASE", defaultOutboxBackoffBase),
		},
	}
}

// Validate returns an error if the configuration cannot be used to
// build a working notifier. The error is human-readable so it can be
// surfaced at server startup.
func (c Config) Validate() error {
	for _, ch := range c.EnabledChannels {
		switch ch {
		case ChannelEmail:
			if !c.Email.Enabled {
				return errors.New("notify: 'email' channel enabled but NOTIFY_EMAIL_ENABLED is false")
			}
			if c.Email.Host == "" {
				return errors.New("notify: 'email' channel requires NOTIFY_SMTP_HOST")
			}
			if c.Email.From == "" {
				return errors.New("notify: 'email' channel requires NOTIFY_SMTP_FROM")
			}
		default:
			return errors.New("notify: unknown channel in NOTIFY_CHANNELS: " + ch)
		}
	}
	if c.Outbox.PollInterval <= 0 {
		return errors.New("notify: NOTIFY_OUTBOX_POLL_INTERVAL must be > 0")
	}
	if c.Outbox.MaxAttempts < 1 {
		return errors.New("notify: NOTIFY_OUTBOX_MAX_ATTEMPTS must be >= 1")
	}
	if c.Outbox.BackoffBase <= 0 {
		return errors.New("notify: NOTIFY_OUTBOX_BACKOFF_BASE must be > 0")
	}
	return nil
}

// splitCSV splits a comma-separated string, trimming whitespace and
// dropping empty parts. Returns nil for an empty input.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			part := trimSpace(s[start:i])
			if part != "" {
				out = append(out, part)
			}
			start = i + 1
		}
	}
	return out
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
