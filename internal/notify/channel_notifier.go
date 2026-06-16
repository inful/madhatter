package notify

import (
	"context"
	"fmt"
	"log/slog"
	"net/mail"
	"sync"

	"github.com/inful/madhatter/internal/database"
)

// ChannelNotifier is the production Notifier implementation. It
// resolves member IDs to email addresses, renders events to text, and
// writes one outbox row per (event, recipient, enabled channel).
//
// The Worker (outbox_worker.go) drains those rows and dispatches them
// to the registered channels; ChannelNotifier never calls a channel
// directly. This decoupling is what makes the producer side fast and
// reliable: handlers return as soon as the row is in the table, and
// the worker handles retries, backoff, and dead-lettering.
type ChannelNotifier struct {
	db       *database.DB
	resolver RecipientResolver
	renderer *renderer
	enabled  map[string]bool
	logger   *slog.Logger
	worker   *Worker
	startOne sync.Once
	stopOnce sync.Once
	stopCh   chan struct{}
	stopped  chan struct{}
}

// NewChannelNotifier returns a Notifier backed by the outbox table.
// enabled lists the channel names that should receive rows (typically
// the value of cfg.EnabledChannels). A nil logger falls back to
// slog.Default().
//
// The returned notifier owns the worker; callers should call Start to
// launch it and Stop to shut it down cleanly.
func NewChannelNotifier(
	db *database.DB,
	resolver RecipientResolver,
	r *renderer,
	worker *Worker,
	enabled []string,
	logger *slog.Logger,
) *ChannelNotifier {
	if logger == nil {
		logger = slog.Default()
	}
	em := make(map[string]bool, len(enabled))
	for _, c := range enabled {
		em[c] = true
	}
	return &ChannelNotifier{
		db:       db,
		resolver: resolver,
		renderer: r,
		enabled:  em,
		logger:   logger,
		worker:   worker,
		stopCh:   make(chan struct{}),
		stopped:  make(chan struct{}),
	}
}

// Start launches the outbox worker in a goroutine. It is safe to call
// once; subsequent calls are no-ops.
func (n *ChannelNotifier) Start(ctx context.Context) {
	n.startOne.Do(func() {
		go func() {
			defer close(n.stopped)
			n.worker.Run(ctx)
		}()
	})
}

// Stop signals the worker to exit and waits for it. Safe to call
// once; subsequent calls are no-ops.
func (n *ChannelNotifier) Stop() {
	n.stopOnce.Do(func() {
		close(n.stopCh)
		<-n.stopped
	})
}

// SwapRequested implements Notifier.
func (n *ChannelNotifier) SwapRequested(ctx context.Context, e SwapEvent) {
	n.enqueue(ctx, EventSwapRequested, e, []recipientRef{{id: e.TargetMemberID, name: e.TargetName}})
}

// SwapAccepted implements Notifier.
func (n *ChannelNotifier) SwapAccepted(ctx context.Context, e SwapEvent) {
	n.enqueue(ctx, EventSwapAccepted, e, []recipientRef{
		{id: e.RequesterMemberID, name: e.RequesterName},
		{id: e.TargetMemberID, name: e.TargetName},
	})
}

// SwapRejected implements Notifier.
func (n *ChannelNotifier) SwapRejected(ctx context.Context, e SwapEvent) {
	n.enqueue(ctx, EventSwapRejected, e, []recipientRef{{id: e.RequesterMemberID, name: e.RequesterName}})
}

// SwapCancelled implements Notifier.
func (n *ChannelNotifier) SwapCancelled(ctx context.Context, e SwapEvent) {
	n.enqueue(ctx, EventSwapCancelled, e, []recipientRef{{id: e.TargetMemberID, name: e.TargetName}})
}

// WFHStateChanged implements Notifier.
func (n *ChannelNotifier) WFHStateChanged(ctx context.Context, e WFHEvent) {
	n.enqueue(ctx, EventWFHStateChange, e, []recipientRef{{id: e.MemberID, name: e.MemberName}})
}

// CoverAssigned implements Notifier.
func (n *ChannelNotifier) CoverAssigned(ctx context.Context, e CoverEvent) {
	n.enqueue(ctx, EventCoverAssigned, e, []recipientRef{{id: e.CoverMemberID, name: e.CoverMemberName}})
}

// UserPendingApproval implements Notifier. The event is fanned out to
// every active admin (rather than a single recipient), so this method
// resolves the admin list directly instead of going through enqueue's
// per-recipient shape.
func (n *ChannelNotifier) UserPendingApproval(ctx context.Context, e UserPendingApprovalEvent) {
	if n.resolver == nil {
		return
	}
	admins, err := n.resolver.ListActiveAdmins(ctx)
	if err != nil || len(admins) == 0 {
		return
	}
	refs := make([]recipientRef, 0, len(admins))
	for _, a := range admins {
		refs = append(refs, recipientRef{id: a.ID, name: a.Name})
	}
	n.enqueue(ctx, EventUserPendingApproval, e, refs)
}

// recipientRef pairs a member id with a name hint. The name hint lets
// callers pass the name they already loaded (e.g. from a swap join)
// without forcing the notifier to re-query.
type recipientRef struct {
	id   string
	name string
}

// enqueue resolves each recipient, renders the event, and writes one
// outbox row per enabled channel. Recipient resolution failures are
// logged and skipped. Outbox write failures are logged and dropped
// (we do not block the calling handler). Recipients that have
// disabled email are silently skipped.
func (n *ChannelNotifier) enqueue(ctx context.Context, eventKind string, event any, recipients []recipientRef) {
	for _, r := range recipients {
		email, name, ok := n.resolveRecipient(ctx, eventKind, r)
		if !ok {
			continue
		}
		subject, body, err := n.renderer.render(eventKind, event, r.id)
		if err != nil {
			n.logger.Warn("notify: render failed",
				slog.String("event_kind", eventKind),
				slog.String("err", err.Error()))
			continue
		}

		// unsubscribeURL is filled in by the renderer; the email
		// channel stamps it on the List-Unsubscribe header at
		// send time. Default to "" so non-email channels are not
		// confused.
		unsubscribeURL := ""
		if n.renderer.unsubscribeFn != nil {
			unsubscribeURL = n.renderer.unsubscribeFn(r.id)
		}

		for channelName := range n.enabled {
			if _, err := n.db.EnqueueOutboxEntry(ctx,
				eventKind, channelName, email, name, subject, body, unsubscribeURL,
			); err != nil {
				n.logger.Warn("notify: enqueue outbox row failed",
					slog.String("event_kind", eventKind),
					slog.String("channel", channelName),
					slog.String("recipient", email),
					slog.String("err", err.Error()))
			}
		}
	}
}

// resolveRecipient looks up the email and display name for a
// recipient and checks whether they have unsubscribed. Returns
// (email, name, true) on success; on any failure (unknown id,
// empty or malformed email, unsubscribed) it logs the reason and
// returns (ok=false) so the caller can continue. A preference-
// lookup error is treated as "enabled" so a transient DB blip
// doesn't silently drop notifications.
//
// The malformed-email check uses net/mail.ParseAddress, the same
// parser the email channel uses downstream. Catching the
// rejection at enqueue time turns a 5-retry, 30-minute failure
// loop into an immediate skip — important for data-entry
// problems (e.g. a stray env-var name typed into the team form)
// that would otherwise burn outbox rows for hours.
func (n *ChannelNotifier) resolveRecipient(ctx context.Context, eventKind string, r recipientRef) (email, name string, ok bool) {
	email, name, err := n.resolver.ResolveByID(ctx, r.id)
	if err != nil || email == "" {
		n.logger.Warn("notify: skip unknown recipient",
			slog.String("event_kind", eventKind),
			slog.String("member_id", r.id),
			slog.String("err", errMsg(err)))
		return "", "", false
	}
	if _, parseErr := mail.ParseAddress(email); parseErr != nil {
		n.logger.Warn("notify: skip recipient with malformed email",
			slog.String("event_kind", eventKind),
			slog.String("member_id", r.id),
			slog.String("email", email),
			slog.String("err", parseErr.Error()))
		return "", "", false
	}
	enabled, err := n.resolver.EmailEnabled(ctx, r.id)
	if err != nil {
		n.logger.Warn("notify: preference lookup failed; defaulting to enabled",
			slog.String("event_kind", eventKind),
			slog.String("member_id", r.id),
			slog.String("err", err.Error()))
		enabled = true
	}
	if !enabled {
		n.logger.Debug("notify: skip unsubscribed recipient",
			slog.String("event_kind", eventKind),
			slog.String("member_id", r.id))
		return "", "", false
	}
	if r.name != "" {
		name = r.name
	}
	return email, name, true
}

func errMsg(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprint(err)
}
