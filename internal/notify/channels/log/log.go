// Package log provides a Channel implementation that records every
// outbound message to the structured logger and returns nil. It is
// used by LogNotifier for --development mode and by tests that want to
// assert on what the notifier tried to send.
package log

import (
	"context"
	"log/slog"

	"github.com/inful/madhatter/internal/notify/channels"
)

// Channel writes each message to slog at info level and returns nil.
type Channel struct {
	logger *slog.Logger
}

// New returns a Channel that uses the provided logger. A nil logger
// falls back to slog.Default().
func New(logger *slog.Logger) *Channel {
	if logger == nil {
		logger = slog.Default()
	}
	return &Channel{logger: logger}
}

// Name implements channels.Channel.
func (c *Channel) Name() string { return "log" }

// Send implements channels.Channel. It never returns an error.
func (c *Channel) Send(_ context.Context, msg channels.OutboundMessage) error {
	c.logger.Info("notification (log channel)",
		slog.String("event_kind", msg.EventKind),
		slog.String("recipient", msg.Recipient),
		slog.String("recipient_name", msg.RecipientName),
		slog.String("subject", msg.Subject),
		slog.String("body", msg.Body),
	)
	return nil
}
