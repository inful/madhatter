package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotificationOutbox_EnqueueAndGet(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	id, err := db.EnqueueOutboxEntry(ctx,
		"test.event",
		OutboxChannelEmail,
		"alice@example.com",
		"Alice",
		"Test subject",
		"Test body",
	)
	require.NoError(t, err)
	require.NotEmpty(t, id)

	got, err := db.GetOutboxEntry(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, id, got.ID)
	assert.Equal(t, "test.event", got.EventKind)
	assert.Equal(t, OutboxChannelEmail, got.Channel)
	assert.Equal(t, "alice@example.com", got.Recipient)
	assert.Equal(t, "Alice", got.RecipientName)
	assert.Equal(t, "Test subject", got.Subject)
	assert.Equal(t, "Test body", got.Body)
	assert.Equal(t, 0, got.Attempts)
	assert.Equal(t, OutboxStatusPending, got.Status)
	assert.Nil(t, got.SentAt)
}

func TestNotificationOutbox_EnqueueEmptyRecipientName_StoredAsEmpty(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	id, err := db.EnqueueOutboxEntry(ctx,
		"test.event",
		OutboxChannelEmail,
		"alice@example.com",
		"", // no display name
		"Subject",
		"Body",
	)
	require.NoError(t, err)

	got, err := db.GetOutboxEntry(ctx, id)
	require.NoError(t, err)
	assert.Empty(t, got.RecipientName)
}

func TestNotificationOutbox_ClaimDue_OnlyReturnsDueRows(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// One row that's due now.
	dueID, err := db.EnqueueOutboxEntry(ctx, "e", OutboxChannelEmail, "a@x", "A", "s", "b")
	require.NoError(t, err)

	// One row whose next_attempt_at is in the future (manually pushed).
	futureID, err := db.EnqueueOutboxEntry(ctx, "e", OutboxChannelEmail, "b@x", "B", "s", "b")
	require.NoError(t, err)
	future := time.Now().Add(time.Hour)
	require.NoError(t, db.MarkOutboxFailed(ctx, futureID, "transient", future))

	claimed, err := db.ClaimDueOutboxEntries(ctx, 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	assert.Equal(t, dueID, claimed[0].ID)
}

func TestNotificationOutbox_ClaimDue_RespectsLimit(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	for range 5 {
		_, err := db.EnqueueOutboxEntry(ctx, "e", OutboxChannelEmail, "a@x", "A", "s", "b")
		require.NoError(t, err)
	}

	claimed, err := db.ClaimDueOutboxEntries(ctx, 2)
	require.NoError(t, err)
	assert.Len(t, claimed, 2)
}

func TestNotificationOutbox_MarkSent_TransitionsToSent(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	id, err := db.EnqueueOutboxEntry(ctx, "e", OutboxChannelEmail, "a@x", "A", "s", "b")
	require.NoError(t, err)

	require.NoError(t, db.MarkOutboxSent(ctx, id))

	got, err := db.GetOutboxEntry(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, OutboxStatusSent, got.Status)
	assert.NotNil(t, got.SentAt)
	assert.Empty(t, got.LastError)
}

func TestNotificationOutbox_MarkFailed_IncrementsAttemptsAndSchedulesRetry(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	id, err := db.EnqueueOutboxEntry(ctx, "e", OutboxChannelEmail, "a@x", "A", "s", "b")
	require.NoError(t, err)

	next := time.Now().Add(5 * time.Minute)
	require.NoError(t, db.MarkOutboxFailed(ctx, id, "smtp timeout", next))

	got, err := db.GetOutboxEntry(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, OutboxStatusPending, got.Status, "should stay pending after a transient failure")
	assert.Equal(t, 1, got.Attempts)
	assert.Equal(t, "smtp timeout", got.LastError)
	assert.WithinDuration(t, next, got.NextAttemptAt, time.Second)
}

func TestNotificationOutbox_MarkDead_TransitionsToDead(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	id, err := db.EnqueueOutboxEntry(ctx, "e", OutboxChannelEmail, "a@x", "A", "s", "b")
	require.NoError(t, err)

	now := time.Now()
	require.NoError(t, db.MarkOutboxDead(ctx, id, "max attempts exceeded", now))

	got, err := db.GetOutboxEntry(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, OutboxStatusDead, got.Status)
	assert.Equal(t, 1, got.Attempts)
	assert.Equal(t, "max attempts exceeded", got.LastError)
}

func TestNotificationOutbox_FailedRowsAreDueAfterNextAttempt(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	id, err := db.EnqueueOutboxEntry(ctx, "e", OutboxChannelEmail, "a@x", "A", "s", "b")
	require.NoError(t, err)

	// Schedule a retry in the past; the row should be picked up.
	past := time.Now().Add(-time.Minute)
	require.NoError(t, db.MarkOutboxFailed(ctx, id, "transient", past))

	claimed, err := db.ClaimDueOutboxEntries(ctx, 10)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	assert.Equal(t, id, claimed[0].ID)
	assert.Equal(t, 1, claimed[0].Attempts)
}

func TestNotificationOutbox_DeadRowsAreNotClaimed(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	id, err := db.EnqueueOutboxEntry(ctx, "e", OutboxChannelEmail, "a@x", "A", "s", "b")
	require.NoError(t, err)
	require.NoError(t, db.MarkOutboxDead(ctx, id, "gave up", time.Now()))

	claimed, err := db.ClaimDueOutboxEntries(ctx, 10)
	require.NoError(t, err)
	assert.Empty(t, claimed)
}

func TestNotificationOutbox_Constants(t *testing.T) {
	// Sanity check the exposed constants so a typo doesn't silently
	// break config validation.
	assert.Equal(t, "email", OutboxChannelEmail)
	assert.Equal(t, "pending", OutboxStatusPending)
	assert.Equal(t, "sent", OutboxStatusSent)
	assert.Equal(t, "dead", OutboxStatusDead)
}
