//nolint:goconst // test fixtures intentionally reuse sample names, dates, and IDs
package notify

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/notify/channels"
)

// recordingChannel is a channels.Channel that records every Send call.
type recordingChannel struct {
	mu    sync.Mutex
	sent  []channels.OutboundMessage
	failN int // first failN calls return err
	calls int
}

func (c *recordingChannel) Name() string { return "test" }
func (c *recordingChannel) Send(_ context.Context, msg channels.OutboundMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.calls <= c.failN {
		return assert.AnError
	}
	c.sent = append(c.sent, msg)
	return nil
}

func (c *recordingChannel) Sent() []channels.OutboundMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]channels.OutboundMessage, len(c.sent))
	copy(out, c.sent)
	return out
}

type stubResolver struct {
	byID map[string]fakeRecipient
}

func (s stubResolver) ResolveByID(_ context.Context, id string) (string, string, error) {
	r, ok := s.byID[id]
	if !ok {
		return "", "", errUnknownMember
	}
	return r.email, r.name, nil
}

var errUnknownMember = stringError("unknown")

type stringError string

func (e stringError) Error() string { return string(e) }

func TestChannelNotifier_EnqueuesOneRowPerRecipientPerChannel(t *testing.T) {
	db, cleanup := setupNotifyDB(t)
	defer cleanup()
	ctx := context.Background()

	resolver := stubResolver{byID: map[string]fakeRecipient{
		"tgt": {email: "tgt@example.com", name: "Target"},
	}}
	r, err := newRenderer("https://x")
	require.NoError(t, err)

	ch := &recordingChannel{}
	worker := NewWorker(db, NewStaticResolver(ch), OutboxConfig{
		PollInterval: time.Hour, MaxAttempts: 5, BackoffBase: time.Second,
	}, nil)
	n := NewChannelNotifier(db, resolver, r, worker, []string{"test"}, nil)

	n.SwapRequested(ctx, SwapEvent{
		RequesterMemberID: "req",
		RequesterName:     "Requester",
		TargetMemberID:    "tgt",
		TargetName:        "Target",
		RequesterDate:     "2026-07-01",
		TargetDate:        "2026-07-15",
	})

	rows, err := db.ClaimDueOutboxEntries(ctx, 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, EventSwapRequested, rows[0].EventKind)
	assert.Equal(t, "tgt@example.com", rows[0].Recipient)
	assert.Equal(t, "test", rows[0].Channel)
	assert.Contains(t, rows[0].Subject, "Requester")
	assert.Contains(t, rows[0].Body, "Target")
}

func TestChannelNotifier_UnknownRecipient_Skipped(t *testing.T) {
	db, cleanup := setupNotifyDB(t)
	defer cleanup()
	ctx := context.Background()

	resolver := stubResolver{byID: map[string]fakeRecipient{}}
	r, err := newRenderer("https://x")
	require.NoError(t, err)

	ch := &recordingChannel{}
	worker := NewWorker(db, NewStaticResolver(ch), OutboxConfig{
		PollInterval: time.Hour, MaxAttempts: 5, BackoffBase: time.Second,
	}, nil)
	n := NewChannelNotifier(db, resolver, r, worker, []string{"test"}, nil)

	// Should not panic, should not enqueue anything.
	n.SwapRequested(ctx, SwapEvent{
		RequesterMemberID: "req",
		TargetMemberID:    "missing",
		RequesterDate:     "2026-07-01",
		TargetDate:        "2026-07-15",
	})

	rows, err := db.ClaimDueOutboxEntries(ctx, 10)
	require.NoError(t, err)
	assert.Empty(t, rows)
}

func TestChannelNotifier_StartStopIsIdempotent(t *testing.T) {
	db, cleanup := setupNotifyDB(t)
	defer cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r, err := newRenderer("https://x")
	require.NoError(t, err)
	ch := &recordingChannel{}
	worker := NewWorker(db, NewStaticResolver(ch), OutboxConfig{
		PollInterval: time.Hour, MaxAttempts: 5, BackoffBase: time.Second,
	}, nil)
	n := NewChannelNotifier(db, stubResolver{}, r, worker, []string{"test"}, nil)
	n.Start(ctx)
	n.Start(ctx) // no-op
	cancel()
	n.Stop()
	n.Stop() // no-op
}

// setupNotifyDB creates a temp database for notify tests.
func setupNotifyDB(t *testing.T) (*database.DB, func()) {
	t.Helper()
	dir := t.TempDir()
	db, err := database.New(dir + "/test.db")
	require.NoError(t, err)
	return db, func() { _ = db.Close() }
}
