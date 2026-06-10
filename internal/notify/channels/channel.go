package channels

import "context"

// Channel is a single delivery mechanism (SMTP, Slack, HTTP webhook, etc.).
// The notifier hands each outbox row to the channel whose Name() matches
// the row's channel column.
type Channel interface {
	// Name returns the stable identifier used in outbox rows and
	// configuration. Examples: "email", "slack", "msteams".
	Name() string

	// Send delivers the message. It MUST respect ctx cancellation so the
	// outbox worker can shut down promptly. It returns nil on success
	// and a non-nil error on any failure the worker should record and
	// retry; the error string is persisted to notification_outbox.last_error.
	Send(ctx context.Context, msg OutboundMessage) error
}

// OutboundMessage is the channel-agnostic payload the worker hands to a
// channel. The Subject and Body have already been rendered from a
// text/template — channels should treat them as opaque strings.
type OutboundMessage struct {
	EventKind     string
	Recipient     string
	RecipientName string
	Subject       string
	Body          string

	// Headers is an optional set of additional message headers.
	// Channels that don't support arbitrary headers (most don't)
	// should ignore it; channels that do (email) apply it to the
	// outbound message. Keys are canonical (e.g. "List-Unsubscribe").
	Headers map[string]string

	// UnsubscribeURL is the one-click unsubscribe URL for the
	// recipient. The email channel uses it both as a List-Unsubscribe
	// header value (RFC 8058) and embeds it in the rendered body
	// footer by way of the renderer. Other channels can ignore it.
	UnsubscribeURL string
}
