package notify

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/notify/channels"
)

// TestChannelNotifier_EndToEnd_WritesOutboxRowsForRealRecipient drives
// the full producer path: a swap.requested event with a real team
// member, the production ChannelNotifier writing through the real
// db.EnqueueOutboxEntry, the renderer using the production renderer,
// and a fake channel registered to confirm the resolver found the
// email. Asserts on the resulting outbox row content.
func TestChannelNotifier_EndToEnd_WritesOutboxRowsForRealRecipient(t *testing.T) {
	db, cleanup := setupNotifyDB(t)
	defer cleanup()
	ctx := context.Background()

	// Real team member so the recipient resolver can find an email.
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	// Real renderer and worker, with a fake channel.
	r, err := NewRenderer("https://rota.example.com", nil)
	require.NoError(t, err)
	fc := &endToEndChannel{name: "test"}
	worker := NewWorker(db, NewStaticResolver(fc), OutboxConfig{
		PollInterval: 1, MaxAttempts: 3, BackoffBase: 1,
	}, nil)
	notifier := NewChannelNotifier(db, dbRecipientResolver{db: db}, r, worker, []string{"test"}, nil)

	notifier.SwapRequested(ctx, SwapEvent{
		SwapID:            "swap-1",
		RequesterMemberID: aliceID,
		RequesterName:     "Alice",
		TargetMemberID:    bobID,
		TargetName:        "Bob",
		RequesterDate:     "2026-07-01",
		TargetDate:        "2026-07-15",
		ActorName:         "system",
	})

	// One outbox row, addressed to Bob, with rendered content.
	rows, err := db.ClaimDueOutboxEntries(ctx, 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)

	got := rows[0]
	assert.Equal(t, EventSwapRequested, got.EventKind)
	assert.Equal(t, "test", got.Channel)
	assert.Equal(t, "bob@example.com", got.Recipient)
	assert.Equal(t, "Bob", got.RecipientName)
	assert.Contains(t, got.Subject, "Alice", "subject should mention the requester")
	assert.Contains(t, got.Body, "Bob", "body should mention the target by name")
	assert.Contains(t, got.Body, "2026-07-01")
	assert.Contains(t, got.Body, "2026-07-15")
	assert.Contains(t, got.Body, "https://rota.example.com/swaps",
		"body should include the BaseURL")
}

// TestChannelNotifier_EndToEnd_TwoEnabledChannels_WritesTwoRows
// verifies the per-service-rows decision: when the notifier is
// configured with two enabled channels, a single event for one
// recipient produces two outbox rows.
func TestChannelNotifier_EndToEnd_TwoEnabledChannels_WritesTwoRows(t *testing.T) {
	db, cleanup := setupNotifyDB(t)
	defer cleanup()
	ctx := context.Background()

	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	r, err := NewRenderer("https://x", nil)
	require.NoError(t, err)
	emailCh := &endToEndChannel{name: "email"}
	logCh := &endToEndChannel{name: "log"}
	worker := NewWorker(db, NewStaticResolver(emailCh, logCh), OutboxConfig{
		PollInterval: 1, MaxAttempts: 3, BackoffBase: 1,
	}, nil)
	notifier := NewChannelNotifier(db, dbRecipientResolver{db: db}, r, worker,
		[]string{"email", "log"}, nil)

	notifier.CoverAssigned(ctx, CoverEvent{
		LeaveID:         "leave-1",
		LeaveMemberID:   memberID,
		LeaveMemberName: "Alice",
		CoverMemberID:   memberID,
		CoverMemberName: "Alice",
		StartDate:       "2026-09-01",
		EndDate:         "2026-09-05",
	})

	rows, err := db.ClaimDueOutboxEntries(ctx, 10)
	require.NoError(t, err)
	require.Len(t, rows, 2)

	channels := map[string]bool{}
	for _, r := range rows {
		assert.Equal(t, EventCoverAssigned, r.EventKind)
		assert.Equal(t, "alice@example.com", r.Recipient)
		channels[r.Channel] = true
	}
	assert.True(t, channels["email"])
	assert.True(t, channels["log"])
}

// endToEndChannel is a channels.Channel fixture for end-to-end
// integration tests. It records the OutboundMessages it was asked
// to send so tests can assert on the worker's dispatch path.
type endToEndChannel struct {
	name string
	mu   sync.Mutex
	sent []channels.OutboundMessage
}

func (c *endToEndChannel) Name() string { return c.name }
func (c *endToEndChannel) Send(_ context.Context, msg channels.OutboundMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, msg)
	return nil
}

// TestResolver_RoundTrip_WorkerDeliversOutboxRows closes the loop:
// the notifier writes a row, the worker drains it, the fake
// channel receives the rendered message. This is the "smoke test"
// for the whole pipeline.
func TestResolver_RoundTrip_WorkerDeliversOutboxRows(t *testing.T) {
	db, cleanup := setupNotifyDB(t)
	defer cleanup()
	ctx := context.Background()

	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	r, err := NewRenderer("https://x", nil)
	require.NoError(t, err)
	ch := &endToEndChannel{name: "test"}
	worker := NewWorker(db, NewStaticResolver(ch), OutboxConfig{
		PollInterval: 1, MaxAttempts: 3, BackoffBase: 1,
	}, nil)
	notifier := NewChannelNotifier(db, dbRecipientResolver{db: db}, r, worker,
		[]string{"test"}, nil)

	notifier.WFHStateChanged(ctx, WFHEvent{
		RequestID:  "wfh-1",
		MemberID:   memberID,
		MemberName: "Alice",
		Date:       "2026-08-01",
		OldStatus:  "pending",
		NewStatus:  "approved",
		ActorName:  "system",
	})

	// Drive one drain synchronously (no time.Sleep needed).
	worker.drain(ctx)

	// Row should be marked 'sent' now.
	rows, err := db.ClaimDueOutboxEntries(ctx, 10)
	require.NoError(t, err)
	assert.Empty(t, rows, "row should be in 'sent' state, not claimable")

	// The fake channel should have received exactly one message with
	// the rendered subject and body.
	ch.mu.Lock()
	defer ch.mu.Unlock()
	require.Len(t, ch.sent, 1)
	msg := ch.sent[0]
	assert.Equal(t, EventWFHStateChange, msg.EventKind)
	assert.Equal(t, "alice@example.com", msg.Recipient)
	assert.Equal(t, "Alice", msg.RecipientName)
	assert.Contains(t, msg.Subject, "approved")
	assert.Contains(t, msg.Body, "2026-08-01")
}

// dbRecipientResolver is a small adapter used only by the
// end-to-end tests. It mirrors the production type in
// internal/api/server.go but lives here so this test file doesn't
// need to import the api package (which would pull in everything).
type dbRecipientResolver struct {
	db *database.DB
}

func (r dbRecipientResolver) ResolveByID(ctx context.Context, memberID string) (string, string, error) {
	m, err := r.db.GetMemberByID(ctx, memberID)
	if err != nil {
		return "", "", err
	}
	if m == nil {
		return "", "", errNotFound
	}
	return m.Email, m.Name, nil
}

// EmailEnabled implements notify.RecipientResolver by looking up
// the notification_preferences row. Mirrors the production
// dbRecipientResolver.
func (r dbRecipientResolver) EmailEnabled(ctx context.Context, memberID string) (bool, error) {
	if memberID == "" {
		return true, nil
	}
	return r.db.IsNotificationEmailEnabled(ctx, memberID)
}

// ListActiveAdmins implements notify.RecipientResolver by
// mirroring the production path in internal/api/server.go.
func (r dbRecipientResolver) ListActiveAdmins(ctx context.Context) ([]AdminRef, error) {
	users, err := r.db.GetQueries().ListAdminUsers(ctx)
	if err != nil {
		return nil, err
	}
	refs := make([]AdminRef, 0, len(users))
	for i := range users {
		refs = append(refs, AdminRef{ID: users[i].ID, Name: users[i].Name})
	}
	return refs, nil
}

var errNotFound = memberNotFoundError("member not found")

type memberNotFoundError string

func (e memberNotFoundError) Error() string { return string(e) }
