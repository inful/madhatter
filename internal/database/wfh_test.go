package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

	// Recurring-row cases: the materializer creates auto-approved rows
	// with is_recurring=1. Users must be able to self-withdraw these
	// just like ad-hoc rows; the IsRecurring flag is metadata that
	// describes *why* the row exists, not a gate on the withdrawal.
	// A regression here would either silently disable user control over
	// recurring days (the help page documents that self-withdrawal
	// works) or, worse, re-materialize a withdrawn row and undo the
	// user's choice.
	//
	// Each subtest seeds its own fresh member so the prior ad-hoc
	// subtests' rows don't pollute the (member, date) unique
	// constraint or the row count.
	t.Run("owner can withdraw future recurring request", func(t *testing.T) {
		carolID, err := db.AddTeamMember(ctx, "Carol", "carol@example.com")
		require.NoError(t, err)

		date := futureDay(25)
		require.NoError(t, db.CreateApprovedRecurringWFHRequest(ctx, carolID, date, time.Now().UTC()))

		rows, err := db.GetWFHRequestsByMember(ctx, carolID)
		require.NoError(t, err)
		require.Len(t, rows, 1, "fresh member must have exactly one row")
		recurringID := rows[0].ID
		require.True(t, rows[0].IsRecurring, "seeded row must have is_recurring=1")
		require.Equal(t, WFHStatusApproved, rows[0].Status)

		err = db.WithdrawOwnWFHRequest(ctx, recurringID, carolID)
		require.NoError(t, err, "recurring rows must be self-withdrawable just like ad-hoc rows")

		got, err := db.GetWFHRequestByID(ctx, recurringID)
		require.NoError(t, err)
		assert.Equal(t, WFHStatusWithdrawn, got.Status, "status flips to withdrawn")
		assert.True(t, got.IsRecurring, "IsRecurring flag must be preserved so the audit trail still records that this was a recurring occurrence the user opted out of")
		assert.Nil(t, got.WithdrawnBy, "self-withdraw must leave withdrawn_by NULL — the admin-withdraw path is the only one that records an actor")
	})

	t.Run("owner can withdraw recurring request for today", func(t *testing.T) {
		// Use Dave so the (member, date) unique constraint on
		// dateToday doesn't collide with Alice's earlier ad-hoc
		// "today" row.
		daveID, err := db.AddTeamMember(ctx, "Dave", "dave@example.com")
		require.NoError(t, err)

		require.NoError(t, db.CreateApprovedRecurringWFHRequest(ctx, daveID, dateToday, time.Now().UTC()))

		rows, err := db.GetWFHRequestsByMember(ctx, daveID)
		require.NoError(t, err)
		require.Len(t, rows, 1)

		err = db.WithdrawOwnWFHRequest(ctx, rows[0].ID, daveID)
		require.NoError(t, err, "today's recurring row must be withdrawable")

		got, err := db.GetWFHRequestByID(ctx, rows[0].ID)
		require.NoError(t, err)
		assert.Equal(t, WFHStatusWithdrawn, got.Status)
		assert.True(t, got.IsRecurring, "IsRecurring flag preserved")
	})

	// Note: the second half of the recurring-withdraw contract —
	// that the materializer won't re-insert a withdrawn recurring
	// row — is already pinned by
	// TestEnsureRecurringMaterialized_PreservesWithdrawnRow in
	// internal/wfh/recurring_materializer_test.go. The database
	// layer only cares that WithdrawOwnWFHRequest flips the status
	// correctly; the materializer's skip-on-existing-row logic is
	// orthogonal.
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

// seedWFHRow inserts a wfh_requests row directly through the SQLC layer,
// bypassing CreateWFHRequest's past-date guard. Used by purge tests that
// need historical rows.
func seedWFHRow(t *testing.T, db *DB, ctx context.Context, id, memberID, date, status string) {
	t.Helper()
	_, err := db.GetQueries().CreateWFHRequest(ctx, sqlc.CreateWFHRequestParams{
		ID:       id,
		MemberID: memberID,
		Date:     parseDate(t, date),
	})
	require.NoError(t, err)
	if status != WFHStatusPending {
		require.NoError(t, db.UpdateWFHRequestStatus(ctx, id, status))
	}
}

func TestCountWFHRequestsBefore(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupTestDB(t)
	defer cleanup()

	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	// Cutoff at 2026-03-15 — rows with date < cutoff should count.
	cutoff := "2026-03-15"
	seedWFHRow(t, db, ctx, "before-1", memberID, "2026-03-10", WFHStatusApproved)
	seedWFHRow(t, db, ctx, "before-2", memberID, "2026-03-14", WFHStatusDenied)
	// Cutoff itself is NOT < cutoff, so it must not be counted.
	seedWFHRow(t, db, ctx, "at-cutoff", memberID, "2026-03-15", WFHStatusApproved)
	seedWFHRow(t, db, ctx, "after-1", memberID, "2026-03-16", WFHStatusPending)
	seedWFHRow(t, db, ctx, "after-2", memberID, "2026-04-01", WFHStatusApproved)

	count, err := db.CountWFHRequestsBefore(ctx, cutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(2), count, "only strictly-before rows should be counted")
}

func TestCountWFHRequestsBefore_InvalidDate(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := db.CountWFHRequestsBefore(ctx, "not-a-date")
	require.ErrorIs(t, err, ErrWFHInvalidDate)
}

func TestPurgeWFHRequestsBefore_DeletesOnlyPastRows(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupTestDB(t)
	defer cleanup()

	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	// Mix of statuses on both sides of the cutoff. Purge must delete
	// strictly-past rows regardless of status, and preserve the rest.
	cutoff := "2026-03-15"
	seedWFHRow(t, db, ctx, "past-pending", memberID, "2026-03-10", WFHStatusPending)
	seedWFHRow(t, db, ctx, "past-approved", memberID, "2026-03-12", WFHStatusApproved)
	seedWFHRow(t, db, ctx, "past-denied", memberID, "2026-03-13", WFHStatusDenied)
	seedWFHRow(t, db, ctx, "past-withdrawn", memberID, "2026-03-14", WFHStatusWithdrawn)
	// Cutoff itself must survive (boundary check).
	seedWFHRow(t, db, ctx, "at-cutoff-approved", memberID, "2026-03-15", WFHStatusApproved)
	seedWFHRow(t, db, ctx, "after-pending", memberID, "2026-03-20", WFHStatusPending)
	seedWFHRow(t, db, ctx, "after-approved", memberID, "2026-04-01", WFHStatusApproved)

	deleted, err := db.PurgeWFHRequestsBefore(ctx, cutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(4), deleted, "only the four strictly-past rows must be deleted")

	// The boundary row and the two future rows must still exist.
	_, err = db.GetWFHRequestByID(ctx, "at-cutoff-approved")
	require.NoError(t, err)
	_, err = db.GetWFHRequestByID(ctx, "after-pending")
	require.NoError(t, err)
	_, err = db.GetWFHRequestByID(ctx, "after-approved")
	require.NoError(t, err)

	// The deleted rows must be gone — GetWFHRequestByID returns ErrWFHNotFound.
	for _, id := range []string{"past-pending", "past-approved", "past-denied", "past-withdrawn"} {
		_, err := db.GetWFHRequestByID(ctx, id)
		require.ErrorIs(t, err, ErrWFHNotFound, "row %s should be purged", id)
	}
}

func TestPurgeWFHRequestsBefore_NoMatchingRowsReturnsZero(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupTestDB(t)
	defer cleanup()

	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	// Only future rows exist; nothing should be purged.
	seedWFHRow(t, db, ctx, "future", memberID, "2026-12-31", WFHStatusApproved)

	deleted, err := db.PurgeWFHRequestsBefore(ctx, "2026-01-01")
	require.NoError(t, err)
	assert.Equal(t, int64(0), deleted)

	// Future row survives.
	_, err = db.GetWFHRequestByID(ctx, "future")
	require.NoError(t, err)
}

func TestPurgeWFHRequestsBefore_InvalidDate(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := db.PurgeWFHRequestsBefore(ctx, "not-a-date")
	require.ErrorIs(t, err, ErrWFHInvalidDate)
}

// TestWFHErrorFor_KnownSentinels pins every WFH sentinel error to its
// transport-level status and user-facing message. The api and web layers
// consult this table, so adding a new ErrWFH* without a row here will
// silently fall back to a 500.
func TestWFHErrorFor_KnownSentinels(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantMsg    string
	}{
		{"NotFound", ErrWFHNotFound, 404, "WFH request not found."},
		{"NotOwner", ErrWFHNotOwner, 403, "You can only modify your own WFH requests."},
		{"AlreadySettled", ErrWFHAlreadySettled, 409, "This WFH request has already been settled and cannot be cancelled."},
		{"DuplicateRequest", ErrWFHDuplicateRequest, 409, "A WFH request already exists for this date."},
		{"InvalidDate", ErrWFHInvalidDate, 422, "invalid date format, expected YYYY-MM-DD"},
		{"DatePassed", ErrWFHDatePassed, 422, "This WFH day has already passed."},
		{"DateTooFar", ErrWFHDateTooFar, 422, "WFH requests can only be made up to a limited number of days in advance."},
		{"MemberNotFound", ErrWFHMemberNotFound, 422, "Member not found."},
		{"RecurringContractDay", ErrWFHRecurringContractDay, 409, "This date falls on your contractual recurring WFH day."},
		// ErrWFHPermanentMember is an alias for ErrWFHRecurringContractDay; errors.Is must still match.
		{"PermanentMember alias", ErrWFHPermanentMember, 409, "This date falls on your contractual recurring WFH day."},
		{"OnHoliday", ErrWFHOnHoliday, 422, "WFH requests cannot be made for holidays."},
		{"NotApproved", ErrWFHNotApproved, 409, "Only approved WFH requests can be withdrawn."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			info, ok := WFHErrorFor(tc.err)
			require.True(t, ok, "WFHErrorFor must recognize %T as a WFH sentinel", tc.err)
			assert.Equal(t, tc.wantStatus, info.Status)
			assert.Equal(t, tc.wantMsg, info.Message)
		})
	}
}

// TestWFHErrorFor_WrappedSentinel ensures WFHErrorFor unwraps via errors.Is
// so handlers can pass through fmt.Errorf("%w", sentinel, ...) chains
// without losing the mapping.
func TestWFHErrorFor_WrappedSentinel(t *testing.T) {
	t.Parallel()

	wrapped := fmt.Errorf("context: %w", ErrWFHNotFound)

	info, ok := WFHErrorFor(wrapped)
	require.True(t, ok)
	assert.Equal(t, 404, info.Status)
	assert.Equal(t, "WFH request not found.", info.Message)
}

// TestWFHErrorFor_UnknownError ensures non-WFH errors return ok=false so the
// caller can decide on a generic fallback (typically a 500).
func TestWFHErrorFor_UnknownError(t *testing.T) {
	t.Parallel()

	info, ok := WFHErrorFor(errors.New("some unrelated failure"))
	require.False(t, ok)
	assert.Empty(t, info.Status)
	assert.Empty(t, info.Message)
}

// TestWFHErrorFor_Nil confirms nil doesn't panic and reports no match.
func TestWFHErrorFor_Nil(t *testing.T) {
	t.Parallel()

	info, ok := WFHErrorFor(nil)
	require.False(t, ok)
	assert.Empty(t, info.Status)
}

// TestWFHFromSQLCFields pins the sql-null → domain conversion so future
// refactors of the sqlc adapter don't silently lose pointer fields or
// flip IsRecurring.
func TestWFHFromSQLCFields(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 15, 9, 30, 0, 0, time.UTC)
	earlier := time.Date(2026, 3, 14, 17, 0, 0, 0, time.UTC)
	date := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	actor := "admin-1"

	cases := []struct {
		name string
		in   wfhFields
		want WFHRequest
	}{
		{
			name: "all optional fields invalid",
			in: wfhFields{
				ID: "row-1", MemberID: "alice", Date: date,
				Status: WFHStatusApproved, IsRecurring: 1,
			},
			want: WFHRequest{
				ID: "row-1", MemberID: "alice", Date: "2026-05-20",
				Status: WFHStatusApproved, IsRecurring: true,
				CreatedAt: time.Time{}, SettledAt: nil, WithdrawnBy: nil, WithdrawnAt: nil,
			},
		},
		{
			name: "IsRecurring zero is false",
			in: wfhFields{
				ID: "row-2", MemberID: "bob", Date: date,
				Status: WFHStatusPending, IsRecurring: 0,
			},
			want: WFHRequest{
				ID: "row-2", MemberID: "bob", Date: "2026-05-20",
				Status: WFHStatusPending, IsRecurring: false,
				CreatedAt: time.Time{}, SettledAt: nil, WithdrawnBy: nil, WithdrawnAt: nil,
			},
		},
		{
			name: "IsRecurring nonzero non-one is false",
			in: wfhFields{
				ID: "row-3", MemberID: "carol", Date: date,
				Status: WFHStatusApproved, IsRecurring: 7,
			},
			want: WFHRequest{
				ID: "row-3", MemberID: "carol", Date: "2026-05-20",
				Status: WFHStatusApproved, IsRecurring: false,
				CreatedAt: time.Time{}, SettledAt: nil, WithdrawnBy: nil, WithdrawnAt: nil,
			},
		},
		{
			name: "all optional fields valid",
			in: wfhFields{
				ID: "row-4", MemberID: "dave", Date: date,
				Status:      WFHStatusWithdrawn,
				CreatedAt:   sql.NullTime{Time: now, Valid: true},
				SettledAt:   sql.NullTime{Time: earlier, Valid: true},
				WithdrawnBy: sql.NullString{String: actor, Valid: true},
				WithdrawnAt: sql.NullTime{Time: now, Valid: true},
				IsRecurring: 1,
			},
			want: WFHRequest{
				ID: "row-4", MemberID: "dave", Date: "2026-05-20",
				Status:      WFHStatusWithdrawn,
				CreatedAt:   now,
				SettledAt:   &earlier,
				WithdrawnBy: &actor,
				WithdrawnAt: &now,
				IsRecurring: true,
			},
		},
		{
			name: "Date formats as YYYY-MM-DD regardless of timezone input",
			in: wfhFields{
				ID: "row-5", MemberID: "eve", Date: date,
				Status: WFHStatusApproved,
			},
			want: WFHRequest{
				ID: "row-5", MemberID: "eve", Date: "2026-05-20",
				Status: WFHStatusApproved, CreatedAt: time.Time{},
				SettledAt: nil, WithdrawnBy: nil, WithdrawnAt: nil,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := wfhFromSQLCFields(tc.in)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestWFHFromSQLCFields_PointersIndependent ensures the optional pointer
// fields are not aliased across calls — a regression would mean callers
// holding a slice of WFHRequest see later writes mutate earlier entries.
func TestWFHFromSQLCFields_PointersIndependent(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 15, 9, 30, 0, 0, time.UTC)
	earlier := time.Date(2026, 3, 14, 17, 0, 0, 0, time.UTC)

	base := wfhFields{
		ID: "row-1", MemberID: "alice", Date: now,
		Status:      WFHStatusApproved,
		SettledAt:   sql.NullTime{Time: earlier, Valid: true},
		WithdrawnBy: sql.NullString{String: "admin", Valid: true},
		WithdrawnAt: sql.NullTime{Time: now, Valid: true},
	}

	first := wfhFromSQLCFields(base)
	second := wfhFromSQLCFields(base)

	require.NotNil(t, first.SettledAt)
	require.NotNil(t, second.SettledAt)
	*first.SettledAt = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	*first.WithdrawnAt = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	*first.WithdrawnBy = "tampered"

	assert.Equal(t, earlier, *second.SettledAt, "second SettledAt must not alias first")
	assert.Equal(t, now, *second.WithdrawnAt, "second WithdrawnAt must not alias first")
	assert.Equal(t, "admin", *second.WithdrawnBy, "second WithdrawnBy must not alias first")
}
