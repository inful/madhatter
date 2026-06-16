package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateApprovedRecurringWFHRequest(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	future := time.Now().UTC().AddDate(0, 0, 10).Format("2006-01-02")

	t.Run("creates approved recurring row", func(t *testing.T) {
		settledAt := time.Now().UTC()
		require.NoError(t, db.CreateApprovedRecurringWFHRequest(ctx, memberID, future, settledAt))

		row, err := db.GetWFHRequestByID(ctx, "")
		_ = row
		_ = err
		// The materializer's job is the insert; round-trip via list.
		rows, err := db.GetWFHRequestsByMember(ctx, memberID)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, future, rows[0].Date)
		assert.Equal(t, WFHStatusApproved, rows[0].Status)
		assert.True(t, rows[0].IsRecurring)
	})

	t.Run("duplicate date returns ErrWFHDuplicateRequest", func(t *testing.T) {
		// Different member, same date — not a duplicate by (member, date).
		otherMemberID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
		require.NoError(t, err)
		// (member, future) doesn't exist for Bob; should succeed.
		require.NoError(t, db.CreateApprovedRecurringWFHRequest(ctx, otherMemberID, future, time.Now().UTC()))

		// Same (member, future) twice — duplicate.
		err = db.CreateApprovedRecurringWFHRequest(ctx, otherMemberID, future, time.Now().UTC())
		require.ErrorIs(t, err, ErrWFHDuplicateRequest)
	})

	t.Run("past date returns ErrWFHDatePassed", func(t *testing.T) {
		past := time.Now().UTC().AddDate(0, 0, -3).Format("2006-01-02")
		err := db.CreateApprovedRecurringWFHRequest(ctx, memberID, past, time.Now().UTC())
		require.ErrorIs(t, err, ErrWFHDatePassed)
	})

	t.Run("invalid date returns ErrWFHInvalidDate", func(t *testing.T) {
		err := db.CreateApprovedRecurringWFHRequest(ctx, memberID, "not-a-date", time.Now().UTC())
		require.ErrorIs(t, err, ErrWFHInvalidDate)
	})
}

func TestHasWFHRequestOnDate(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	future := time.Now().UTC().AddDate(0, 0, 10).Format("2006-01-02")

	t.Run("no row returns false", func(t *testing.T) {
		exists, err := db.HasWFHRequestOnDate(ctx, memberID, future)
		require.NoError(t, err)
		assert.False(t, exists)
	})

	t.Run("any row returns true", func(t *testing.T) {
		_, err := db.CreateWFHRequest(ctx, memberID, future)
		require.NoError(t, err)
		exists, err := db.HasWFHRequestOnDate(ctx, memberID, future)
		require.NoError(t, err)
		assert.True(t, exists)
	})

	t.Run("withdrawn row still returns true", func(t *testing.T) {
		// Create then withdraw; the row persists with status=withdrawn,
		// so HasWFHRequestOnDate returns true and the materializer will
		// skip it.
		d := time.Now().UTC().AddDate(0, 0, 20).Format("2006-01-02")
		req, err := db.CreateWFHRequest(ctx, memberID, d)
		require.NoError(t, err)
		require.NoError(t, db.UpdateWFHRequestStatus(ctx, req.ID, WFHStatusApproved))
		require.NoError(t, db.WithdrawOwnWFHRequest(ctx, req.ID, memberID))

		exists, err := db.HasWFHRequestOnDate(ctx, memberID, d)
		require.NoError(t, err)
		assert.True(t, exists)
	})
}
