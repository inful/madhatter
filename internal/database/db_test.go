package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database/sqlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTestDB creates a temporary test database.
func setupTestDB(t *testing.T) (*DB, func()) {
	t.Helper()

	// Create temporary database in temp directory
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := New(dbPath)
	require.NoError(t, err, "Failed to create test database")

	cleanup := func() {
		_ = db.Close()
	}

	return db, cleanup
}

func TestAddTeamMember_Success(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Act
	id, err := db.AddTeamMember(ctx, "Alice Johnson", "alice@example.com")

	// Assert
	require.NoError(t, err)
	require.NotEmpty(t, id)

	// Verify in database
	members, err := db.GetActiveTeamMembers(ctx)
	require.NoError(t, err)
	require.Len(t, members, 1)
	require.Equal(t, "Alice Johnson", members[0].Name)
	require.Equal(t, "alice@example.com", members[0].Email)
	require.True(t, members[0].IsActive)
	require.False(t, members[0].IsPermanentWFH)
	require.False(t, members[0].RecurringWFHMonday)
	require.False(t, members[0].RecurringWFHTuesday)
	require.False(t, members[0].RecurringWFHWednesday)
	require.False(t, members[0].RecurringWFHThursday)
	require.False(t, members[0].RecurringWFHFriday)
}

func TestSetTeamMemberPermanentWFH(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	require.NoError(t, db.SetTeamMemberPermanentWFH(ctx, memberID, true))

	member, err := db.GetMemberByID(ctx, memberID)
	require.NoError(t, err)
	require.True(t, member.IsPermanentWFH)
	require.True(t, member.RecurringWFHMonday)
	require.True(t, member.RecurringWFHTuesday)
	require.True(t, member.RecurringWFHWednesday)
	require.True(t, member.RecurringWFHThursday)
	require.True(t, member.RecurringWFHFriday)

	require.NoError(t, db.SetTeamMemberPermanentWFH(ctx, memberID, false))

	member, err = db.GetMemberByID(ctx, memberID)
	require.NoError(t, err)
	require.False(t, member.IsPermanentWFH)
	require.False(t, member.RecurringWFHMonday)
	require.False(t, member.RecurringWFHTuesday)
	require.False(t, member.RecurringWFHWednesday)
	require.False(t, member.RecurringWFHThursday)
	require.False(t, member.RecurringWFHFriday)
}

func TestSetTeamMemberRecurringWFHDays(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	require.NoError(t, db.SetTeamMemberRecurringWFHDays(ctx, memberID, RecurringWFHDays{
		Monday:   true,
		Thursday: true,
	}))

	member, err := db.GetMemberByID(ctx, memberID)
	require.NoError(t, err)
	require.True(t, member.RecurringWFHMonday)
	require.False(t, member.RecurringWFHTuesday)
	require.False(t, member.RecurringWFHWednesday)
	require.True(t, member.RecurringWFHThursday)
	require.False(t, member.RecurringWFHFriday)
	require.False(t, member.IsPermanentWFH)
}

// TestSetTeamMemberExemptFromAssignment_Roundtrip pins the
// seat-cap-picker exemption flag (migration 000026): default is
// false, set true persists across reads, set false restores the
// default. The picker (step 6 of plans/assigned-wfh-plan.md)
// reads this flag via GetActiveTeamMembers; an exempt member is
// never picked as an involuntary Assigned WFH candidate but their
// voluntary WFHs still count against on-site capacity.
func TestSetTeamMemberExemptFromAssignment_Roundtrip(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	// Default: not exempt.
	m, err := db.GetMemberByID(ctx, memberID)
	require.NoError(t, err)
	require.False(t, m.IsExemptFromAssignment, "default exemption is false")

	// Set true, reads back true.
	require.NoError(t, db.SetTeamMemberExemptFromAssignment(ctx, memberID, true))
	m, err = db.GetMemberByID(ctx, memberID)
	require.NoError(t, err)
	require.True(t, m.IsExemptFromAssignment)

	// Set false, reads back false.
	require.NoError(t, db.SetTeamMemberExemptFromAssignment(ctx, memberID, false))
	m, err = db.GetMemberByID(ctx, memberID)
	require.NoError(t, err)
	require.False(t, m.IsExemptFromAssignment)
}

// TestSetTeamMemberExemptFromAssignment_IndependentOfPermanentWFH
// pins the conceptual split: is_permanent_wfh is a "never on-site"
// flag (set via SetTeamMemberPermanentWFH), is_exempt_from_assignment
// is a "skip in picker rotation" flag. They are independent columns
// on team_members and the setters do not touch each other.
func TestSetTeamMemberExemptFromAssignment_IndependentOfPermanentWFH(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	require.NoError(t, db.SetTeamMemberExemptFromAssignment(ctx, memberID, true))
	m, err := db.GetMemberByID(ctx, memberID)
	require.NoError(t, err)
	require.True(t, m.IsExemptFromAssignment)
	require.False(t, m.IsPermanentWFH, "exemption must not flip the permanent-WFH flag")

	require.NoError(t, db.SetTeamMemberPermanentWFH(ctx, memberID, true))
	m, err = db.GetMemberByID(ctx, memberID)
	require.NoError(t, err)
	require.True(t, m.IsPermanentWFH)
	require.True(t, m.IsExemptFromAssignment, "permanent-WFH must not flip the exemption flag")
}

func TestCreateWFHRequest_RecurringDayReturnsDuplicateAfterMaterialization(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	require.NoError(t, db.SetTeamMemberPermanentWFH(ctx, memberID, true))

	targetDate := nextWeekday(time.Now().UTC(), time.Monday)
	dateStr := targetDate.Format("2006-01-02")

	// Simulate the materializer having run for this period.
	require.NoError(t, db.CreateApprovedRecurringWFHRequest(ctx, memberID, dateStr, time.Now().UTC()))

	// A second ad-hoc request for the same date now collides on UNIQUE
	// (member_id, date) and is rejected with ErrWFHDuplicateRequest.
	request, err := db.CreateWFHRequest(ctx, memberID, dateStr)
	require.ErrorIs(t, err, ErrWFHDuplicateRequest)
	assert.Nil(t, request)
}

func TestCreateWFHRequest_ResurrectsAfterCancel(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	date := nextWeekday(time.Now().UTC(), time.Wednesday).Format("2006-01-02")

	// Step 1: user creates a WFH request.
	original, err := db.CreateWFHRequest(ctx, memberID, date)
	require.NoError(t, err)
	require.Equal(t, WFHStatusPending, original.Status)
	originalID := original.ID
	originalCreatedAt := original.CreatedAt

	// Step 2: user changes their mind and cancels.
	require.NoError(t, db.CancelWFHRequest(ctx, original.ID, memberID))
	cancelled, err := db.GetWFHRequestByID(ctx, original.ID)
	require.NoError(t, err)
	require.Equal(t, WFHStatusCancelled, cancelled.Status)
	require.NotNil(t, cancelled.SettledAt, "settled_at is set when a request is cancelled")

	// Step 3: user changes their mind again and re-requests the same date.
	// Pre-fix this returned ErrWFHDuplicateRequest because the cancelled
	// row still occupies the (member, date) slot. After the fix, the row
	// is resurrected in place — same id, audit fields cleared.
	resurrected, err := db.CreateWFHRequest(ctx, memberID, date)
	require.NoError(t, err, "re-request after cancel must succeed")
	assert.Equal(t, WFHStatusPending, resurrected.Status)
	assert.Equal(t, originalID, resurrected.ID, "resurrected row preserves the original id")
	assert.Equal(t, originalCreatedAt, resurrected.CreatedAt, "resurrected row preserves the original created_at")
	assert.Nil(t, resurrected.SettledAt, "settled_at is cleared on resurrect")
	assert.Nil(t, resurrected.WithdrawnAt, "withdrawn_at is cleared on resurrect")
	assert.Nil(t, resurrected.WithdrawnBy, "withdrawn_by is cleared on resurrect")

	// No second row should have been inserted — the resurrect is in-place.
	all, err := db.GetWFHRequestsByMember(ctx, memberID)
	require.NoError(t, err)
	assert.Len(t, all, 1, "exactly one row exists for the (member, date) pair")
}

func TestCreateWFHRequest_ResurrectsAfterSelfWithdraw(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	date := nextWeekday(time.Now().UTC(), time.Friday).Format("2006-01-02")

	// Step 1: user creates a WFH request that gets approved (e.g. by the
	// settlement run).
	original, err := db.CreateWFHRequest(ctx, memberID, date)
	require.NoError(t, err)
	require.NoError(t, db.UpdateWFHRequestStatus(ctx, original.ID, WFHStatusApproved))
	originalID := original.ID

	// Step 2: user changes their mind and self-withdraws. WithdrawOwnWFHRequest
	// leaves withdrawn_by NULL (the user owns the decision) and stamps
	// withdrawn_at.
	require.NoError(t, db.WithdrawOwnWFHRequest(ctx, original.ID, memberID))
	withdrawn, err := db.GetWFHRequestByID(ctx, original.ID)
	require.NoError(t, err)
	require.Equal(t, WFHStatusWithdrawn, withdrawn.Status)
	assert.Nil(t, withdrawn.WithdrawnBy, "self-withdraw leaves withdrawn_by NULL")
	assert.NotNil(t, withdrawn.WithdrawnAt, "withdrawn_at is stamped on withdrawal")

	// Step 3: user changes their mind again and re-requests the same date.
	// Self-withdrawals are resurrectable (the user owns the decision), so
	// the row flips back to pending in place — same id, audit fields cleared.
	resurrected, err := db.CreateWFHRequest(ctx, memberID, date)
	require.NoError(t, err, "re-request after self-withdraw must succeed")
	assert.Equal(t, WFHStatusPending, resurrected.Status)
	assert.Equal(t, originalID, resurrected.ID, "resurrected row preserves the original id")
	assert.Nil(t, resurrected.SettledAt, "settled_at is cleared on resurrect")
	assert.Nil(t, resurrected.WithdrawnAt, "withdrawn_at is cleared on resurrect")
	assert.Nil(t, resurrected.WithdrawnBy, "withdrawn_by is cleared on resurrect")
	assert.False(t, resurrected.IsRecurring, "resurrected row is not flagged recurring (ad-hoc was 0; resurrect enforces this for the recurring case too)")

	// No second row should have been inserted — the resurrect is in-place.
	all, err := db.GetWFHRequestsByMember(ctx, memberID)
	require.NoError(t, err)
	assert.Len(t, all, 1, "exactly one row exists for the (member, date) pair")
}

func TestCreateWFHRequest_ResurrectsAfterSelfWithdrawOfRecurring(t *testing.T) {
	// Regression guard for a subtle bug: a self-withdrawn recurring row
	// resurrects to status='pending'. If is_recurring were preserved, the
	// resurrected row would be invisible to settlement (which filters
	// is_recurring=0) AND invisible to the materializer (which skips dates
	// that already have any row), leaving the user with a pending request
	// that never settles. Clearing is_recurring on resurrect makes the row
	// a plain ad-hoc pending request that settlement will process.
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	date := nextWeekday(time.Now().UTC(), time.Monday)
	dateStr := date.Format("2006-01-02")

	// Step 1: materializer inserts a contractual recurring row directly.
	require.NoError(t, db.CreateApprovedRecurringWFHRequest(ctx, memberID, dateStr, time.Now().UTC()))
	rows, err := db.GetWFHRequestsByMember(ctx, memberID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	recurring := rows[0]
	require.True(t, recurring.IsRecurring, "seeded row must be flagged recurring")
	require.Equal(t, WFHStatusApproved, recurring.Status)
	originalID := recurring.ID

	// Step 2: Alice self-withdraws the recurring row.
	require.NoError(t, db.WithdrawOwnWFHRequest(ctx, originalID, memberID))
	withdrawn, err := db.GetWFHRequestByID(ctx, originalID)
	require.NoError(t, err)
	require.Equal(t, WFHStatusWithdrawn, withdrawn.Status)
	require.True(t, withdrawn.IsRecurring, "self-withdraw preserves is_recurring on the row")

	// Step 3: Alice changes her mind and re-requests the same date. The
	// resurrect path must clear is_recurring so the resurrected row is
	// treated as ad-hoc and can be picked up by settlement.
	resurrected, err := db.CreateWFHRequest(ctx, memberID, dateStr)
	require.NoError(t, err, "re-request after self-withdraw of a recurring day must succeed")
	assert.Equal(t, WFHStatusPending, resurrected.Status)
	assert.Equal(t, originalID, resurrected.ID, "resurrected row preserves the original id")
	assert.False(t, resurrected.IsRecurring,
		"is_recurring must be cleared so settlement can pick up the resurrected row")

	// The resurrected row must be visible to settlement (filters
	// is_recurring=0). If this changes, the resurrected row is stuck pending.
	pending, err := db.GetPendingForSettlement(ctx, dateStr)
	require.NoError(t, err)
	assert.Len(t, pending, 1, "resurrected recurring row must be visible to settlement as a pending request")
}

func TestCreateWFHRequest_NonCancelledStillDuplicates(t *testing.T) {
	// Regression guard: the resurrect path must not weaken the duplicate
	// semantics for any non-user-owned status (pending, approved, denied,
	// admin-withdrawn). Self-withdrawn rows are intentionally not in this
	// list — they're resurrectable and covered by the dedicated self-
	// withdraw test above.
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	// Create an admin user so the admin-withdrawn subcase can record a real
	// FK in withdrawn_by (the column has a REFERENCES users(id) FK).
	_, err = db.GetQueries().CreateActiveUser(ctx, sqlc.CreateActiveUserParams{
		ID: "admin-1", Email: "admin@example.com", Name: "Admin",
		Provider: "fake", ProviderID: "admin-1",
		IsAdmin: sql.NullInt64{Int64: 1, Valid: true},
	})
	require.NoError(t, err)

	// Use distinct weekdays so each subtest gets a unique date regardless
	// of what day-of-week the test runs on.
	cases := []struct {
		name    string
		weekday time.Weekday
		seed    func(t *testing.T, date string) string
		status  string
	}{
		{
			name:    "pending",
			weekday: time.Thursday,
			seed: func(t *testing.T, date string) string {
				t.Helper()
				req, err := db.CreateWFHRequest(ctx, memberID, date)
				require.NoError(t, err)
				return req.ID
			},
			status: WFHStatusPending,
		},
		{
			name:    "approved",
			weekday: time.Monday,
			seed: func(t *testing.T, date string) string {
				t.Helper()
				req, err := db.CreateWFHRequest(ctx, memberID, date)
				require.NoError(t, err)
				require.NoError(t, db.UpdateWFHRequestStatus(ctx, req.ID, WFHStatusApproved))
				return req.ID
			},
			status: WFHStatusApproved,
		},
		{
			name:    "denied",
			weekday: time.Tuesday,
			seed: func(t *testing.T, date string) string {
				t.Helper()
				req, err := db.CreateWFHRequest(ctx, memberID, date)
				require.NoError(t, err)
				require.NoError(t, db.UpdateWFHRequestStatus(ctx, req.ID, WFHStatusDenied))
				return req.ID
			},
			status: WFHStatusDenied,
		},
		{
			// Admin withdrawal is a final decision — the user must not be
			// able to resurrect it by re-requesting. withdrawn_by IS NOT
			// NULL distinguishes this from self-withdraw.
			name:    "admin-withdrawn",
			weekday: time.Saturday,
			seed: func(t *testing.T, date string) string {
				t.Helper()
				req, err := db.CreateWFHRequest(ctx, memberID, date)
				require.NoError(t, err)
				require.NoError(t, db.UpdateWFHRequestStatus(ctx, req.ID, WFHStatusApproved))
				require.NoError(t, db.WithdrawWFHRequest(ctx, req.ID, "admin-1"))
				return req.ID
			},
			status: WFHStatusWithdrawn,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			date := nextWeekday(time.Now().UTC(), tc.weekday).Format("2006-01-02")
			id := tc.seed(t, date)
			got, err := db.GetWFHRequestByID(ctx, id)
			require.NoError(t, err)
			require.Equal(t, tc.status, got.Status, "seeded row should have the expected status")

			_, err = db.CreateWFHRequest(ctx, memberID, got.Date)
			require.ErrorIs(t, err, ErrWFHDuplicateRequest,
				"non-resurrectable status %q must still surface ErrWFHDuplicateRequest", tc.status)
		})
	}
}

func nextWeekday(start time.Time, weekday time.Weekday) time.Time {
	date := start.AddDate(0, 0, 1)
	for date.Weekday() != weekday {
		date = date.AddDate(0, 0, 1)
	}
	return date
}

func TestCreateWFHRequest_ResurrectRejectsPastDate(t *testing.T) {
	// The resurrect path must honor the same past-date invariant the
	// INSERT path enforces. If a user cancels a request and time passes,
	// re-requesting must not leave a pending row for a date that's now
	// in the past.
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	// Seed a cancelled row for a date that's already in the past — bypasses
	// the past-date guard by going through the queries layer directly, the
	// same way TestWithdrawOwnWFHRequest seeds historical rows.
	pastDate := time.Now().UTC().AddDate(0, 0, -1)
	pastDateStr := pastDate.Format("2006-01-02")
	_, err = db.GetQueries().CreateWFHRequest(ctx, sqlc.CreateWFHRequestParams{
		ID: "cancelled-past-row", MemberID: memberID, Date: pastDate,
	})
	require.NoError(t, err)
	require.NoError(t, db.UpdateWFHRequestStatus(ctx, "cancelled-past-row", WFHStatusCancelled))

	// Re-requesting the past date must be rejected, not resurrected.
	_, err = db.CreateWFHRequest(ctx, memberID, pastDateStr)
	require.ErrorIs(t, err, ErrWFHDatePassed,
		"resurrecting a row for a past date must surface ErrWFHDatePassed")

	// The row must remain in 'cancelled' status — the resurrect was rejected.
	got, err := db.GetWFHRequestByID(ctx, "cancelled-past-row")
	require.NoError(t, err)
	assert.Equal(t, WFHStatusCancelled, got.Status,
		"rejected resurrect must leave the existing row untouched")
}

func TestCreateWFHRequest_ResurrectRejectsHoliday(t *testing.T) {
	// IsHoliday reads from the installed holiday checker, which can change
	// between the original cancel and the re-request. The resurrect path
	// must re-run this check: resurrecting into a date that's now flagged
	// as a holiday would leave a pending WFH on a non-working day.
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	date := nextWeekday(time.Now().UTC(), time.Wednesday)
	dateStr := date.Format("2006-01-02")

	// At submit time, the date is not a holiday; the row is created and
	// then cancelled.
	original, err := db.CreateWFHRequest(ctx, memberID, dateStr)
	require.NoError(t, err)
	require.NoError(t, db.CancelWFHRequest(ctx, original.ID, memberID))

	// Time passes: the date is now on the holiday set.
	db.SetHolidayChecker(func(t time.Time) bool {
		return t.Year() == date.Year() && t.Month() == date.Month() && t.Day() == date.Day()
	})
	t.Cleanup(func() { db.SetHolidayChecker(nil) })

	_, err = db.CreateWFHRequest(ctx, memberID, dateStr)
	require.ErrorIs(t, err, ErrWFHOnHoliday,
		"resurrecting into a date that's now a holiday must surface ErrWFHOnHoliday")

	got, err := db.GetWFHRequestByID(ctx, original.ID)
	require.NoError(t, err)
	assert.Equal(t, WFHStatusCancelled, got.Status,
		"rejected resurrect must leave the existing row untouched")
}

func TestAddTeamMember_DuplicateEmail(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	// Act
	_, err = db.AddTeamMember(ctx, "Alice 2", "alice@example.com")

	// Assert
	require.Error(t, err)
	require.Contains(t, err.Error(), "UNIQUE")
}

func TestAddTeamMember_EmptyName(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Act
	_, err := db.AddTeamMember(ctx, "", "alice@example.com")

	// Assert
	require.Error(t, err)
}

func TestGetActiveTeamMembers_Empty(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Act
	members, err := db.GetActiveTeamMembers(ctx)

	// Assert
	require.NoError(t, err)
	require.Empty(t, members)
}

func TestGetActiveTeamMembers_OnlyActive(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add active members
	id1, _ := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	id2, _ := db.AddTeamMember(ctx, "Bob", "bob@example.com")

	// Deactivate one
	_, err := db.ExecContext(ctx, "UPDATE team_members SET is_active = 0 WHERE id = ?", id1)
	require.NoError(t, err)

	// Act
	members, err := db.GetActiveTeamMembers(ctx)

	// Assert
	require.NoError(t, err)
	require.Len(t, members, 1)
	require.Equal(t, "Bob", members[0].Name)
	require.Equal(t, id2, members[0].ID)
}

func TestGetMemberByEmail_Found(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	// Act
	member, err := db.GetMemberByEmail(ctx, "alice@example.com")

	// Assert
	require.NoError(t, err)
	require.Equal(t, "Alice", member.Name)
	require.Equal(t, "alice@example.com", member.Email)
}

func TestGetMemberByEmail_NotFound(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Act
	_, err := db.GetMemberByEmail(ctx, "nonexistent@example.com")

	// Assert
	require.Error(t, err)
	require.Equal(t, sql.ErrNoRows, err)
}

func TestGetLatestAssignmentDate_Empty(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Act
	latest, err := db.GetLatestAssignmentDate(ctx)

	// Assert
	require.NoError(t, err)
	require.Empty(t, latest)
}

func TestGetLatestAssignmentDate_WithAssignments(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add team members
	id1, _ := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	id2, _ := db.AddTeamMember(ctx, "Bob", "bob@example.com")

	// Add assignments
	today := "2025-01-15"
	tomorrow := "2025-01-16"
	_, err := db.CreateRotaAssignment(ctx, today, id1, false, nil)
	require.NoError(t, err)
	_, err = db.CreateRotaAssignment(ctx, tomorrow, id2, false, nil)
	require.NoError(t, err)

	// Act
	latest, err := db.GetLatestAssignmentDate(ctx)

	// Assert
	require.NoError(t, err)
	require.Equal(t, tomorrow, latest)
}

func TestGetAssignmentsByDateRange(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add team members
	id1, _ := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	id2, _ := db.AddTeamMember(ctx, "Bob", "bob@example.com")

	// Add assignments
	startDate := "2025-01-15"
	midDate := "2025-01-16"
	endDate := "2025-01-17"
	_, err := db.CreateRotaAssignment(ctx, startDate, id1, false, nil)
	require.NoError(t, err)
	_, err = db.CreateRotaAssignment(ctx, midDate, id2, false, nil)
	require.NoError(t, err)
	_, err = db.CreateRotaAssignment(ctx, endDate, id1, false, nil)
	require.NoError(t, err)

	// Act - get range including all dates
	assignments, err := db.GetAssignmentsByDateRange(ctx, startDate, endDate)

	// Assert
	require.NoError(t, err)
	require.Len(t, assignments, 3)

	// Act - get partial range
	assignments, err = db.GetAssignmentsByDateRange(ctx, startDate, midDate)

	// Assert
	require.NoError(t, err)
	require.Len(t, assignments, 2)
}

func TestCreateBackup_ReturnsSQLiteSnapshot(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	backupBytes, err := db.CreateBackup(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, backupBytes)
	require.True(t, strings.HasPrefix(string(backupBytes), "SQLite format 3\x00"))
}

func TestValidateRestoreCandidate_ValidBackup(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	backupBytes, err := db.CreateBackup(ctx)
	require.NoError(t, err)

	err = db.ValidateRestoreCandidate(ctx, backupBytes)
	require.NoError(t, err)
}

func TestValidateRestoreCandidate_InvalidBytes(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	err := db.ValidateRestoreCandidate(context.Background(), []byte("not-a-sqlite-file"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "valid SQLite")
}

func TestApplyRestoreCandidate_RestoresPreviousState(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	backupBytes, err := db.CreateBackup(ctx)
	require.NoError(t, err)

	_, err = db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	err = db.ApplyRestoreCandidate(ctx, backupBytes)
	require.NoError(t, err)

	members, err := db.GetActiveTeamMembers(ctx)
	require.NoError(t, err)
	require.Len(t, members, 1)
	require.Equal(t, "Alice", members[0].Name)
}

func TestValidateRestoreCandidate_OlderBackupVersion_IsAccepted(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	backupBytes, err := db.CreateBackup(ctx)
	require.NoError(t, err)

	liveVersion, _, err := GetMigrationVersion(db.db)
	require.NoError(t, err)
	require.GreaterOrEqual(t, liveVersion, uint(1))

	tmpFile := filepath.Join(t.TempDir(), "older-version.db")
	require.NoError(t, os.WriteFile(tmpFile, backupBytes, 0o600))

	candidateDB, err := sql.Open("sqlite3", tmpFile)
	require.NoError(t, err)

	if liveVersion > 0 {
		// Roll the candidate DB back one version to simulate the
		// "older backup" scenario. Use the highest migration that
		// has a down script available, since migration version
		// numbers can have gaps during phased rollouts (see
		// plans/assigned-wfh-plan.md — migration 25 lands last of
		// the four, leaving a gap between 24 and 26 in Phase 1).
		target := liveVersion - 1
		if !hasMigrationDown(target) {
			target = latestMigrationWithDownBelow(liveVersion)
		}
		if target > 0 {
			require.NoError(t, MigrateToVersion(candidateDB, target))
		}
	}

	require.NoError(t, candidateDB.Close())

	//nolint:gosec // tmpFile is created within t.TempDir and controlled by the test.
	olderBackupBytes, err := os.ReadFile(tmpFile)
	require.NoError(t, err)

	err = db.ValidateRestoreCandidate(ctx, olderBackupBytes)
	require.NoError(t, err)
}

func TestApplyRestoreCandidate_CopyFailure_RollsBack(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	backupBytes, err := db.CreateBackup(ctx)
	require.NoError(t, err)

	_, err = db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	tmpPath, err := writeTempRestoreCandidate(backupBytes)
	require.NoError(t, err)
	defer func() { _ = os.Remove(tmpPath) }()

	conn, err := db.db.Conn(ctx)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	tx, err := conn.BeginTx(ctx, nil)
	require.NoError(t, err)

	tableNames, err := prepareRestoreTransaction(ctx, tx, tmpPath)
	require.NoError(t, err)
	tableNames = append(tableNames, "missing_table")

	err = copyTablesFromRestore(ctx, tx, tableNames)
	require.Error(t, err)
	require.NoError(t, tx.Rollback())

	members, err := db.GetActiveTeamMembers(ctx)
	require.NoError(t, err)
	require.Len(t, members, 2)
}

// hasMigrationDown reports whether a `<N>_<description>.down.sql`
// file exists for the given migration version in the migrations
// directory the runtime resolves (MIGRATIONS_PATH env var or the
// conventional relative path). Used by rollback tests that need
// to skip versions whose down migration is not yet written during
// a phased rollout — see plans/assigned-wfh-plan.md (migration
// 25 lands after 26/27, leaving gaps in Phase 1).
func hasMigrationDown(version uint) bool {
	path, err := getMigrationsPath()
	if err != nil {
		return false
	}
	matches, err := filepath.Glob(filepath.Join(path, fmt.Sprintf("%07d_*.down.sql", version)))
	if err != nil {
		return false
	}
	return len(matches) > 0
}

// latestMigrationWithDownBelow returns the highest version strictly
// below upperLimit that has a down script. Returns 0 if none.
func latestMigrationWithDownBelow(upperLimit uint) uint {
	for v := upperLimit - 1; v > 0; v-- {
		if hasMigrationDown(v) {
			return v
		}
	}
	return 0
}
