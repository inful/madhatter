//nolint:goconst // test fixtures intentionally reuse sample names, dates, and IDs
package notify

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/notify/channels"
)

// workerFakeChannel is a channels.Channel for the worker tests. It
// can be programmed to fail the first N times, then succeed; subsequent
// calls always succeed.
type workerFakeChannel struct {
	mu    sync.Mutex
	name  string
	sent  []channels.OutboundMessage
	failN int
	calls int
}

func (c *workerFakeChannel) Name() string { return c.name }

func (c *workerFakeChannel) Send(_ context.Context, msg channels.OutboundMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.calls <= c.failN {
		return errors.New("simulated failure")
	}
	c.sent = append(c.sent, msg)
	return nil
}

func (c *workerFakeChannel) Sent() []channels.OutboundMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]channels.OutboundMessage, len(c.sent))
	copy(out, c.sent)
	return out
}

func (c *workerFakeChannel) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// enqueueSampleOutbox writes a row directly to the outbox so the
// worker has something to dispatch.
func enqueueSampleOutbox(t *testing.T, db *database.DB) {
	t.Helper()
	_, err := db.EnqueueOutboxEntry(context.Background(),
		"test.event", "test", "alice@example.com", "Recipient", "hi", "hello")
	require.NoError(t, err)
}

// TestWorker_Success_MarksRowSent uses synctest to advance fake time
// past the worker's poll interval deterministically, then asserts the
// row transitioned to 'sent' and the channel recorded the message.
func TestWorker_Success_MarksRowSent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db, cleanup := setupNotifyDB(t)
		defer cleanup()
		ctx := context.Background()

		enqueueSampleOutbox(t, db)

		ch := &workerFakeChannel{name: "test"}
		worker := NewWorker(db, NewStaticResolver(ch), OutboxConfig{
			PollInterval: 50 * time.Millisecond,
			MaxAttempts:  3,
			BackoffBase:  100 * time.Millisecond,
		}, nil)

		// Drive one drain synchronously so we don't depend on the
		// ticker for the happy path.
		worker.drain(ctx)

		got, err := db.GetOutboxEntry(ctx, mustFirstOutboxID(t, db))
		require.NoError(t, err)
		assert.Equal(t, database.OutboxStatusSent, got.Status)
		assert.Equal(t, 1, ch.Calls())
	})
}

// TestWorker_TransientFailure_ReschedulesWithBackoff asserts that a
// failing send increments attempts, sets last_error, and pushes
// next_attempt_at into the future.
func TestWorker_TransientFailure_ReschedulesWithBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db, cleanup := setupNotifyDB(t)
		defer cleanup()
		ctx := context.Background()

		enqueueSampleOutbox(t, db)

		ch := &workerFakeChannel{name: "test", failN: 1} // first call fails
		worker := NewWorker(db, NewStaticResolver(ch), OutboxConfig{
			PollInterval: 50 * time.Millisecond,
			MaxAttempts:  3,
			BackoffBase:  100 * time.Millisecond,
		}, nil)

		before := time.Now()
		worker.drain(ctx)
		after := time.Now()

		id := mustFirstOutboxID(t, db)
		got, err := db.GetOutboxEntry(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, database.OutboxStatusPending, got.Status)
		assert.Equal(t, 1, got.Attempts)
		assert.Equal(t, "simulated failure", got.LastError)
		// First retry should be ~100ms in the future.
		assert.True(t, got.NextAttemptAt.After(before.Add(50*time.Millisecond)),
			"next_attempt_at %v should be > %v", got.NextAttemptAt, before.Add(50*time.Millisecond))
		assert.True(t, got.NextAttemptAt.Before(after.Add(500*time.Millisecond)),
			"next_attempt_at %v should be < %v", got.NextAttemptAt, after.Add(500*time.Millisecond))
	})
}

// TestWorker_MaxAttemptsReached_MarksDead runs a channel that always
// fails, drives MaxAttempts drains, and asserts the row becomes
// 'dead'.
func TestWorker_MaxAttemptsReached_MarksDead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db, cleanup := setupNotifyDB(t)
		defer cleanup()
		ctx := context.Background()

		enqueueSampleOutbox(t, db)

		ch := &workerFakeChannel{name: "test", failN: 9999} // never succeeds
		worker := NewWorker(db, NewStaticResolver(ch), OutboxConfig{
			PollInterval: 50 * time.Millisecond,
			MaxAttempts:  3,
			BackoffBase:  1 * time.Millisecond,
		}, nil)

		// First drain: row is at attempts=0, becomes attempts=1.
		worker.drain(ctx)
		// Advance time past the backoff so the row is claimable again.
		time.Sleep(10 * time.Millisecond)
		worker.drain(ctx)
		// attempts=2 → still pending, retried.
		time.Sleep(10 * time.Millisecond)
		worker.drain(ctx)
		// attempts=3 → MaxAttempts reached → dead.

		id := mustFirstOutboxID(t, db)
		got, err := db.GetOutboxEntry(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, database.OutboxStatusDead, got.Status)
		assert.Equal(t, 3, got.Attempts)
	})
}

// TestWorker_RecoversAfterTransientFailure asserts the worker retries
// until a previously-failing channel starts succeeding, then marks
// the row 'sent'.
func TestWorker_RecoversAfterTransientFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db, cleanup := setupNotifyDB(t)
		defer cleanup()
		ctx := context.Background()

		enqueueSampleOutbox(t, db)

		ch := &workerFakeChannel{name: "test", failN: 2} // first 2 fail, 3rd succeeds
		worker := NewWorker(db, NewStaticResolver(ch), OutboxConfig{
			PollInterval: 50 * time.Millisecond,
			MaxAttempts:  5,
			BackoffBase:  1 * time.Millisecond,
		}, nil)

		worker.drain(ctx) // fail 1
		time.Sleep(10 * time.Millisecond)
		worker.drain(ctx) // fail 2
		time.Sleep(10 * time.Millisecond)
		worker.drain(ctx) // succeed

		id := mustFirstOutboxID(t, db)
		got, err := db.GetOutboxEntry(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, database.OutboxStatusSent, got.Status)
		assert.Equal(t, 2, got.Attempts)
		assert.Empty(t, got.LastError)
	})
}

// TestWorker_UnknownChannel_MarksDead asserts that a row whose
// channel name doesn't match a registered channel is terminally
// failed without retries.
func TestWorker_UnknownChannel_MarksDead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db, cleanup := setupNotifyDB(t)
		defer cleanup()
		ctx := context.Background()

		enqueueSampleOutbox(t, db)

		ch := &workerFakeChannel{name: "registered"}
		worker := NewWorker(db, NewStaticResolver(ch), OutboxConfig{
			PollInterval: 50 * time.Millisecond,
			MaxAttempts:  3,
			BackoffBase:  100 * time.Millisecond,
		}, nil)

		worker.drain(ctx)

		id := mustFirstOutboxID(t, db)
		got, err := db.GetOutboxEntry(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, database.OutboxStatusDead, got.Status)
		assert.Contains(t, got.LastError, "unknown channel")
	})
}

// TestWorker_ExponentialBackoffMatchesFormula sanity-checks the
// backoff math at a few attempt counts.
func TestWorker_ExponentialBackoffMatchesFormula(t *testing.T) {
	db, cleanup := setupNotifyDB(t)
	defer cleanup()

	w := NewWorker(db, NewStaticResolver(&workerFakeChannel{name: "x"}), OutboxConfig{
		BackoffBase: time.Second,
	}, nil)
	cases := map[int]time.Duration{
		1: 1 * time.Second,
		2: 2 * time.Second,
		3: 4 * time.Second,
		4: 8 * time.Second,
	}
	for attempts, want := range cases {
		got := time.Until(w.computeNextAttempt(attempts))
		// Allow a small slack for the time.Now() jitter.
		assert.InDelta(t, want, got, float64(50*time.Millisecond),
			"attempts=%d", attempts)
	}
	// Cap at MaxBackoff.
	got := time.Until(w.computeNextAttempt(20))
	assert.LessOrEqual(t, got, MaxBackoff+50*time.Millisecond)
}

// mustFirstOutboxID returns the ID of the first outbox row in the
// table, failing the test if there is none. It does NOT claim the
// row; callers that have already drained can still find it.
func mustFirstOutboxID(t *testing.T, db *database.DB) string {
	t.Helper()
	rows, err := db.QueryOutboxRowsForTest(context.Background(), 10)
	require.NoError(t, err)
	require.NotEmpty(t, rows, "expected at least one outbox row")
	return rows[0]
}
