package database

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database/sqlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithdrawOwnWFHRequest(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create an admin so the existing admin-path (WithdrawWFHRequest) keeps
	// working. The self-withdraw path doesn't need a user record because
	// withdrawn_by is left NULL for self-withdrawals.
	_, err := db.GetQueries().CreateActiveUser(ctx, sqlc.CreateActiveUserParams{
		ID: "admin-1", Email: "admin@example.com", Name: "Admin",
		Provider: "fake", ProviderID: "admin-1",
		IsAdmin: sql.NullInt64{Int64: 1, Valid: true},
	})
	require.NoError(t, err)

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	// Pick distinct future business days for each subtest, since the
	// (member_id, date) unique constraint prevents repeat dates per member.
	// A `seen` set ensures the weekend-skipping logic doesn't collapse
	// two different offsets onto the same business day.
	seen := make(map[string]bool)
	futureDay := func(daysOut int) string {
		d := time.Now().UTC().AddDate(0, 0, daysOut)
		for {
			if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
				dateStr := d.Format("2006-01-02")
				if !seen[dateStr] {
					seen[dateStr] = true
					return dateStr
				}
			}
			d = d.AddDate(0, 0, 1)
		}
	}

	dateOK := futureDay(10)    // comfortably in the future
	dateToday := todayUTC()    // today is still withdrawable
	dateYesterday := pastDay() // yesterday is past, must be rejected

	t.Run("owner can withdraw future request", func(t *testing.T) {
		req, err := db.CreateWFHRequest(ctx, aliceID, dateOK)
		require.NoError(t, err)
		require.NoError(t, db.UpdateWFHRequestStatus(ctx, req.ID, WFHStatusApproved))

		err = db.WithdrawOwnWFHRequest(ctx, req.ID, aliceID)
		require.NoError(t, err)
		got, err := db.GetWFHRequestByID(ctx, req.ID)
		require.NoError(t, err)
		assert.Equal(t, WFHStatusWithdrawn, got.Status)
	})

	t.Run("owner can withdraw request for today", func(t *testing.T) {
		req, err := db.CreateWFHRequest(ctx, aliceID, dateToday)
		require.NoError(t, err)
		require.NoError(t, db.UpdateWFHRequestStatus(ctx, req.ID, WFHStatusApproved))

		err = db.WithdrawOwnWFHRequest(ctx, req.ID, aliceID)
		require.NoError(t, err)
		got, err := db.GetWFHRequestByID(ctx, req.ID)
		require.NoError(t, err)
		assert.Equal(t, WFHStatusWithdrawn, got.Status)
	})

	t.Run("non-owner cannot withdraw", func(t *testing.T) {
		req, err := db.CreateWFHRequest(ctx, aliceID, futureDay(11))
		require.NoError(t, err)
		require.NoError(t, db.UpdateWFHRequestStatus(ctx, req.ID, WFHStatusApproved))

		err = db.WithdrawOwnWFHRequest(ctx, req.ID, bobID)
		require.ErrorIs(t, err, ErrWFHNotOwner)

		got, err := db.GetWFHRequestByID(ctx, req.ID)
		require.NoError(t, err)
		assert.Equal(t, WFHStatusApproved, got.Status)
	})

	t.Run("past date is rejected", func(t *testing.T) {
		// CreateWFHRequest itself rejects past dates, so seed the
		// past-dated row directly via the queries layer and flip
		// the status to approved.
		reqID := "past-row-id"
		_, err := db.GetQueries().CreateWFHRequest(ctx, sqlc.CreateWFHRequestParams{
			ID:       reqID,
			MemberID: aliceID,
			Date:     parseDate(t, dateYesterday),
		})
		require.NoError(t, err)
		require.NoError(t, db.UpdateWFHRequestStatus(ctx, reqID, WFHStatusApproved))

		err = db.WithdrawOwnWFHRequest(ctx, reqID, aliceID)
		require.ErrorIs(t, err, ErrWFHDatePassed)

		got, err := db.GetWFHRequestByID(ctx, reqID)
		require.NoError(t, err)
		assert.Equal(t, WFHStatusApproved, got.Status,
			"status must not change on a rejected withdraw")
	})

	t.Run("non-approved cannot be withdrawn", func(t *testing.T) {
		req, err := db.CreateWFHRequest(ctx, aliceID, futureDay(20))
		require.NoError(t, err)
		// Status is still 'pending'.

		err = db.WithdrawOwnWFHRequest(ctx, req.ID, aliceID)
		require.ErrorIs(t, err, ErrWFHNotApproved)
	})

	t.Run("non-existent request returns ErrWFHNotFound", func(t *testing.T) {
		err := db.WithdrawOwnWFHRequest(ctx, "no-such-id", aliceID)
		require.ErrorIs(t, err, ErrWFHNotFound)
	})
}

// todayUTC returns today's date in YYYY-MM-DD form, suitable for
// CreateWFHRequest. The WFH date column is a DATE; passing today's
// calendar date in UTC is the right granularity.
func todayUTC() string {
	return time.Now().UTC().Format("2006-01-02")
}

// pastDay returns yesterday's date in YYYY-MM-DD form. Used by
// past-date tests that need a row for a date already in the past.
func pastDay() string {
	return time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
}

// parseDate parses a YYYY-MM-DD string into time.Time at midnight
// UTC, suitable for the WFH date column. Used by tests that
// bypass CreateWFHRequest's past-date check to seed historical
// rows directly via the queries layer.
func parseDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	require.NoError(t, err)
	return d
}
