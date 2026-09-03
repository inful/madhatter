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

// TestGetWFHRequestsVoluntaryInPeriod_ExcludesAssigned pins the
// quota/picker math split: GetWFHRequestsVoluntaryInPeriod must
// exclude rows with origin='assigned', while the original
// GetWFHRequestsUsedInPeriod keeps them. The picker (step 6 of
// plans/assigned-wfh-plan.md) and the user-facing quota counter
// read from the voluntary-only query; the on-site-floor math in
// the existing settlement path reads from the original. The
// underlying SQLC query uses a literal 'assigned' filter, not a
// "everything except ad_hoc", so swapping the seat-cap picker on
// (origin='assigned') doesn't accidentally start excluding
// admin-marked or recurring rows.
func TestGetWFHRequestsVoluntaryInPeriod_ExcludesAssigned(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	// 2 voluntary WFHs in the same period.
	d1 := time.Now().UTC().AddDate(0, 0, 10).Format("2006-01-02")
	d2 := time.Now().UTC().AddDate(0, 0, 11).Format("2006-01-02")
	_, err = db.CreateWFHRequest(ctx, memberID, d1)
	require.NoError(t, err)
	_, err = db.CreateWFHRequest(ctx, memberID, d2)
	require.NoError(t, err)

	// 1 admin-marked WFH on d3. Admin marks use origin='ad_hoc' (the
	// default); only the seat-cap picker inserts origin='assigned'.
	d3 := time.Now().UTC().AddDate(0, 0, 12).Format("2006-01-02")
	require.NoError(t, db.MarkAdminWFH(ctx, "ignored-row-id", memberID, d3, ""))

	// 1 system-assigned WFH on d4. The picker (step 6) inserts these;
	// for the test we insert directly via ExecContext because no
	// public method exists yet. origin='assigned' is the marker.
	d4 := time.Now().UTC().AddDate(0, 0, 13).Format("2006-01-02")
	assignedID := "test-assigned-" + t.Name()
	_, err = db.ExecContext(ctx,
		`INSERT INTO wfh_requests (id, member_id, date, status, origin)
		 VALUES (?, ?, ?, 'approved', 'assigned')`,
		assignedID, memberID, d4)
	require.NoError(t, err)

	periodStart := time.Now().UTC().AddDate(0, 0, 9).Format("2006-01-02")
	periodEnd := time.Now().UTC().AddDate(0, 0, 14).Format("2006-01-02")

	// The original query counts everything (voluntary + admin-marked
	// + assigned): 4 rows. Admin-marked is 'ad_hoc' so it's counted.
	all, err := db.GetWFHRequestsUsedInPeriod(ctx, memberID, periodStart, periodEnd)
	require.NoError(t, err)
	require.Len(t, all, 4, "GetWFHRequestsUsedInPeriod counts every approved/pending row regardless of origin")

	// The voluntary query excludes only origin='assigned': 3 rows
	// (2 voluntary + 1 admin-marked). Admin-marked is origin='ad_hoc'
	// so it remains counted — admin marks are user-facing WFHs that
	// should burn quota (the admin-override feature is explicit,
	// unlike system-assigned which is invisible to the user).
	voluntary, err := db.GetWFHRequestsVoluntaryInPeriod(ctx, memberID, periodStart, periodEnd)
	require.NoError(t, err)
	require.Len(t, voluntary, 3,
		"GetWFHRequestsVoluntaryInPeriod must exclude origin='assigned' only; admin-marked (origin='ad_hoc') still counts")

	// Sanity: the assigned row is in `all` but not in `voluntary`.
	var foundInAll, foundInVoluntary bool
	for _, r := range all {
		if r.Origin == "assigned" {
			foundInAll = true
		}
	}
	for _, r := range voluntary {
		if r.Origin == "assigned" {
			foundInVoluntary = true
		}
	}
	assert.True(t, foundInAll, "origin='assigned' row must appear in GetWFHRequestsUsedInPeriod")
	assert.False(t, foundInVoluntary, "origin='assigned' row must NOT appear in GetWFHRequestsVoluntaryInPeriod")
}
