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
	_, err := db.GetQueries().CreateUser(ctx, sqlc.CreateUserParams{
		ID: "admin-1", Email: "admin@example.com", Name: "Admin",
		Provider: "fake", ProviderID: "admin-1",
		IsAdmin:  sql.NullInt64{Int64: 1, Valid: true},
		IsActive: sql.NullInt64{Int64: 1, Valid: true},
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

	dateOK := futureDay(10)  // 24h withdrawal deadline still future
	dateNear := futureDay(1) // 24h deadline already passed

	t.Run("owner can withdraw", func(t *testing.T) {
		req, err := db.CreateWFHRequest(ctx, aliceID, dateOK)
		require.NoError(t, err)
		require.NoError(t, db.UpdateWFHRequestStatus(ctx, req.ID, WFHStatusApproved))

		err = db.WithdrawOwnWFHRequest(ctx, req.ID, aliceID, 24)
		require.NoError(t, err)
		got, err := db.GetWFHRequestByID(ctx, req.ID)
		require.NoError(t, err)
		assert.Equal(t, WFHStatusWithdrawn, got.Status)
	})

	t.Run("non-owner cannot withdraw", func(t *testing.T) {
		req, err := db.CreateWFHRequest(ctx, aliceID, futureDay(11))
		require.NoError(t, err)
		require.NoError(t, db.UpdateWFHRequestStatus(ctx, req.ID, WFHStatusApproved))

		err = db.WithdrawOwnWFHRequest(ctx, req.ID, bobID, 24)
		require.ErrorIs(t, err, ErrWFHNotOwner)

		got, err := db.GetWFHRequestByID(ctx, req.ID)
		require.NoError(t, err)
		assert.Equal(t, WFHStatusApproved, got.Status)
	})

	t.Run("deadline enforced", func(t *testing.T) {
		req, err := db.CreateWFHRequest(ctx, aliceID, dateNear)
		require.NoError(t, err)
		require.NoError(t, db.UpdateWFHRequestStatus(ctx, req.ID, WFHStatusApproved))

		err = db.WithdrawOwnWFHRequest(ctx, req.ID, aliceID, 24)
		require.ErrorIs(t, err, ErrWFHWithdrawalDeadlinePassed)
	})

	t.Run("non-approved cannot be withdrawn", func(t *testing.T) {
		req, err := db.CreateWFHRequest(ctx, aliceID, futureDay(20))
		require.NoError(t, err)
		// Status is still 'pending'.

		err = db.WithdrawOwnWFHRequest(ctx, req.ID, aliceID, 24)
		require.ErrorIs(t, err, ErrWFHNotApproved)
	})

	t.Run("non-existent request returns ErrWFHNotFound", func(t *testing.T) {
		err := db.WithdrawOwnWFHRequest(ctx, "no-such-id", aliceID, 24)
		require.ErrorIs(t, err, ErrWFHNotFound)
	})
}
