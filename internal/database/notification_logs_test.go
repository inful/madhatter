package database

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTryCreateNotificationLog_DedupesByKindAndDate(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	id1, created1, err := db.TryCreateNotificationLog(ctx, "teams_oncall_tomorrow", "2026-01-12", "", "", "msg")
	require.NoError(t, err)
	require.True(t, created1)
	require.NotEmpty(t, id1)

	id2, created2, err := db.TryCreateNotificationLog(ctx, "teams_oncall_tomorrow", "2026-01-12", "", "", "msg")
	require.NoError(t, err)
	require.False(t, created2)
	require.Empty(t, id2)
}

func TestTryCreateNotificationLog_DeleteAllowsRecreate(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	id, created, err := db.TryCreateNotificationLog(ctx, "teams_oncall_tomorrow", "2026-01-12", "", "", "msg")
	require.NoError(t, err)
	require.True(t, created)
	require.NotEmpty(t, id)

	require.NoError(t, db.DeleteNotificationLog(ctx, id))

	id2, created2, err := db.TryCreateNotificationLog(ctx, "teams_oncall_tomorrow", "2026-01-12", "", "", "msg")
	require.NoError(t, err)
	require.True(t, created2)
	require.NotEmpty(t, id2)
}

func TestTryCreateNotificationLog_ValidatesInput(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	_, _, err := db.TryCreateNotificationLog(ctx, "", "2026-01-12", "", "", "")
	require.Error(t, err)

	_, _, err = db.TryCreateNotificationLog(ctx, "teams_oncall_tomorrow", "", "", "", "")
	require.Error(t, err)

	_, _, err = db.TryCreateNotificationLog(ctx, "teams_oncall_tomorrow", "not-a-date", "", "", "")
	require.Error(t, err)
}

func TestDeleteNotificationLog_EmptyID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	require.Error(t, db.DeleteNotificationLog(ctx, ""))
}
