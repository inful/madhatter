// Package notify provides the application-facing notification API and the
// delivery plumbing for sending events to external channels (email today,
// Slack / Teams / etc. tomorrow).
//
// The architecture is split into four layers, from outer-most to
// inner-most:
//
//  1. Producer code (web handlers, the WFH service, the rota engine) calls
//     the high-level Notifier interface. It does not know about channels,
//     templates, SMTP, or the outbox.
//
//  2. The Notifier implementation resolves member IDs to recipient addresses,
//     renders an event to (subject, body) text via the renderer, and writes
//     one outbox row per (event, recipient, channel).
//
//  3. The outbox worker drains the table, claims due rows, and dispatches
//     them to the registered channel matching the row's channel name.
//
//  4. Each Channel.Send does whatever its delivery mechanism requires
//     (SMTP, HTTP, etc.) and returns an error so the worker can mark the
//     row failed and retry with backoff.
//
// Handlers never block on network I/O. If SMTP is down, the user-visible
// flow still succeeds; the worker retries the message until it succeeds
// or hits the max-attempts threshold.
package notify

// EventKind values written to notification_outbox.event_kind. Kept as
// named constants so call sites don't scatter magic strings.
const (
	EventSwapRequested  = "swap.requested"
	EventSwapAccepted   = "swap.accepted"
	EventSwapRejected   = "swap.rejected"
	EventSwapCancelled  = "swap.cancelled"
	EventWFHStateChange = "wfh.state_changed"
	EventCoverAssigned  = "cover.assigned"
)

// ChannelName values used in NOTIFY_CHANNELS and the
// notification_outbox.channel column. Mirrors database.OutboxChannel*
// but lives here so callers can pass channel names without importing
// the database package.
const (
	ChannelEmail = "email"
	ChannelLog   = "log"
	// ChannelSlack is reserved for a future Slack channel implementation.
	// Listed here so callers can refer to it by name in tests/configs
	// without scattering magic strings.
	ChannelSlack = "slack"
)
