package notify

import (
	"context"
	"sync"
)

// Notifier is the public API producer code uses. Every method is
// non-blocking from the caller's perspective: production implementations
// write to the outbox, LogNotifier dispatches in-process for tests and
// --development mode.
//
// Methods NEVER return an error. Failures land in the outbox, the worker
// retries, and any unrecoverable situation is logged. This keeps swap
// accept / WFH approve / leave cover flows from failing just because
// SMTP is down.
type Notifier interface {
	SwapRequested(ctx context.Context, e SwapEvent)
	SwapAccepted(ctx context.Context, e SwapEvent)
	SwapRejected(ctx context.Context, e SwapEvent)
	SwapCancelled(ctx context.Context, e SwapEvent)
	WFHStateChanged(ctx context.Context, e WFHEvent)
	CoverAssigned(ctx context.Context, e CoverEvent)
}

// LogNotifier is a Notifier that calls a registered set of channels
// directly. It is used in --development mode and in tests that want to
// assert on what the system tried to send without standing up a worker
// goroutine or the production outbox table.
type LogNotifier struct {
	channels []channelEntry
	mu       sync.RWMutex
	resolver RecipientResolver
}

// channelEntry pairs a channel with a predicate that decides whether the
// channel should receive a given event. We keep it simple: in v1 the
// predicate is just "is this channel enabled in config". Future channels
// may have per-event filtering.
type channelEntry struct {
	channel registeredChannel
	enabled bool
}

// registeredChannel is the subset of channels.Channel that LogNotifier
// needs. We keep it as a local type alias to avoid pulling channels into
// the public surface of this package.
type registeredChannel interface {
	Name() string
	Send(ctx context.Context, msg outboundMessage) error
}

// outboundMessage is the channel-agnostic payload. We redefine it here
// so callers don't need to import the channels package; in the worker
// path, the worker hands a channels.OutboundMessage to the channel.
type outboundMessage struct {
	EventKind     string
	Recipient     string
	RecipientName string
	Subject       string
	Body          string
}

// RecipientResolver turns a member_id into a recipient. The production
// resolver looks up team_members.email; the test resolver can return
// canned values.
//
// EmailEnabled is a separate hook so the production resolver can
// consult notification_preferences (a small per-event cache cost)
// without the test resolver having to implement the lookup. The
// zero-value behavior is "enabled" so resolvers that only implement
// ResolveByID continue to work.
type RecipientResolver interface {
	ResolveByID(ctx context.Context, memberID string) (email, name string, err error)
	// EmailEnabled returns false when the member has unsubscribed
	// from email notifications. Implementations that don't track
	// preferences can leave this at the default (return true).
	EmailEnabled(ctx context.Context, memberID string) (bool, error)
}

// NewLogNotifier returns a LogNotifier that calls the given channels
// directly via the provided resolver. The resolver is invoked once per
// recipient per event; the notifier is safe to call from multiple
// goroutines.
//
// Pass an empty enabledChans to receive messages on every registered
// channel; otherwise only channels whose Name() appears in enabledChans
// are called.
func NewLogNotifier(resolver RecipientResolver, enabledChans map[string]bool, chans ...registeredChannel) *LogNotifier {
	n := &LogNotifier{
		resolver: resolver,
	}
	for _, ch := range chans {
		n.channels = append(n.channels, channelEntry{
			channel: ch,
			enabled: len(enabledChans) == 0 || enabledChans[ch.Name()],
		})
	}
	return n
}

// SwapRequested implements Notifier.
func (n *LogNotifier) SwapRequested(ctx context.Context, e SwapEvent) {
	n.dispatch(ctx, EventSwapRequested, []recipientTarget{
		{memberID: e.TargetMemberID, nameHint: e.TargetName},
	}, func(ch registeredChannel, msg outboundMessage) error {
		return ch.Send(ctx, msg)
	}, e)
}

// SwapAccepted implements Notifier.
func (n *LogNotifier) SwapAccepted(ctx context.Context, e SwapEvent) {
	n.dispatch(ctx, EventSwapAccepted, []recipientTarget{
		{memberID: e.RequesterMemberID, nameHint: e.RequesterName},
		{memberID: e.TargetMemberID, nameHint: e.TargetName},
	}, func(ch registeredChannel, msg outboundMessage) error {
		return ch.Send(ctx, msg)
	}, e)
}

// SwapRejected implements Notifier.
func (n *LogNotifier) SwapRejected(ctx context.Context, e SwapEvent) {
	n.dispatch(ctx, EventSwapRejected, []recipientTarget{
		{memberID: e.RequesterMemberID, nameHint: e.RequesterName},
	}, func(ch registeredChannel, msg outboundMessage) error {
		return ch.Send(ctx, msg)
	}, e)
}

// SwapCancelled implements Notifier.
func (n *LogNotifier) SwapCancelled(ctx context.Context, e SwapEvent) {
	n.dispatch(ctx, EventSwapCancelled, []recipientTarget{
		{memberID: e.TargetMemberID, nameHint: e.TargetName},
	}, func(ch registeredChannel, msg outboundMessage) error {
		return ch.Send(ctx, msg)
	}, e)
}

// WFHStateChanged implements Notifier.
func (n *LogNotifier) WFHStateChanged(ctx context.Context, e WFHEvent) {
	n.dispatch(ctx, EventWFHStateChange, []recipientTarget{
		{memberID: e.MemberID, nameHint: e.MemberName},
	}, func(ch registeredChannel, msg outboundMessage) error {
		return ch.Send(ctx, msg)
	}, e)
}

// CoverAssigned implements Notifier.
func (n *LogNotifier) CoverAssigned(ctx context.Context, e CoverEvent) {
	n.dispatch(ctx, EventCoverAssigned, []recipientTarget{
		{memberID: e.CoverMemberID, nameHint: e.CoverMemberName},
	}, func(ch registeredChannel, msg outboundMessage) error {
		return ch.Send(ctx, msg)
	}, e)
}

// recipientTarget is the internal shape used to drive dispatch — a
// member id plus a name hint so we don't have to re-resolve the name
// from the database when the caller already had it.
type recipientTarget struct {
	memberID string
	nameHint string
}

// dispatch resolves each recipient, builds a (subject, body) for the
// event via the default text renderer, and calls every enabled channel.
// Channel failures are logged but never propagated; the notifier is
// best-effort.
func (n *LogNotifier) dispatch(
	ctx context.Context,
	eventKind string,
	recipients []recipientTarget,
	send func(registeredChannel, outboundMessage) error,
	event any,
) {
	n.mu.RLock()
	channels := n.channels
	resolver := n.resolver
	n.mu.RUnlock()

	for _, r := range recipients {
		email, name, err := resolver.ResolveByID(ctx, r.memberID)
		if err != nil || email == "" {
			// Skip silently; production log would have a structured
			// warning, but LogNotifier is the test/dev path.
			continue
		}
		enabled, _ := resolver.EmailEnabled(ctx, r.memberID)
		if !enabled {
			continue
		}
		if r.nameHint != "" {
			name = r.nameHint
		}
		subject, body, err := renderEvent(eventKind, event, r.memberID)
		if err != nil {
			continue
		}
		msg := outboundMessage{
			EventKind:     eventKind,
			Recipient:     email,
			RecipientName: name,
			Subject:       subject,
			Body:          body,
		}
		for _, ch := range channels {
			if !ch.enabled {
				continue
			}
			_ = send(ch.channel, msg)
		}
	}
}
