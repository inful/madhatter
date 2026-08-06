package web

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupPresenceTestDB(t *testing.T) (*database.DB, func()) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "presence.db")

	db, err := database.New(dbPath)
	require.NoError(t, err)

	cleanup := func() {
		_ = db.Close()
	}

	return db, cleanup
}

func TestGetUpcomingPresenceFrom_SkipsNonBusinessDays(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupPresenceTestDB(t)
	defer cleanup()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	_, err = db.CreateLeaveRecord(ctx, bobID, "2024-01-08", "2024-01-08")
	require.NoError(t, err)

	holidayChecker := func(date time.Time) bool {
		holiday := time.Date(2024, time.January, 9, 0, 0, 0, 0, time.UTC)
		return date.Equal(holiday)
	}

	handler := &Handler{db: db, holidayChecker: holidayChecker}
	start := time.Date(2024, time.January, 5, 0, 0, 0, 0, time.UTC)

	presence, err := handler.getUpcomingPresenceFrom(ctx, start)
	require.NoError(t, err)
	require.Len(t, presence, 10)

	require.Equal(t, "2024-01-05", presence[0].DateISO)
	require.Equal(t, "2024-01-08", presence[1].DateISO)
	require.Equal(t, "2024-01-10", presence[2].DateISO)
	require.Equal(t, "2024-01-11", presence[3].DateISO)
	require.Equal(t, "2024-01-12", presence[4].DateISO)
	require.Equal(t, "2024-01-15", presence[5].DateISO)
	require.Equal(t, "2024-01-16", presence[6].DateISO)
	require.Equal(t, "2024-01-17", presence[7].DateISO)
	require.Equal(t, "2024-01-18", presence[8].DateISO)
	require.Equal(t, "2024-01-19", presence[9].DateISO)

	require.Len(t, presence[0].Present, 2)
	require.Len(t, presence[1].Present, 1)
	require.Equal(t, "Alice", presence[1].Present[0].Name)
	require.Len(t, presence[1].Away, 1)
	require.Equal(t, "Bob", presence[1].Away[0].Member.Name)
}

func TestGetUpcomingPresenceFrom_ShowsSwapBadgeForSwappedAssignment(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupPresenceTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	start := testutil.NextBusinessDay(time.Now().AddDate(0, 0, 7))
	next := testutil.NextBusinessDay(start.AddDate(0, 0, 1))

	aliceAssignmentID, err := db.CreateRotaAssignment(ctx, start.Format("2006-01-02"), aliceID, false, nil)
	require.NoError(t, err)
	bobAssignmentID, err := db.CreateRotaAssignment(ctx, next.Format("2006-01-02"), bobID, false, nil)
	require.NoError(t, err)

	swapID, err := db.CreateHatSwap(ctx, aliceAssignmentID, bobAssignmentID, aliceID, bobID)
	require.NoError(t, err)
	require.NoError(t, db.ExecuteSwap(ctx, swapID))

	handler := &Handler{db: db}
	presence, err := handler.getUpcomingPresenceFrom(ctx, start)
	require.NoError(t, err)
	require.NotEmpty(t, presence)

	require.Equal(t, start.Format("2006-01-02"), presence[0].DateISO)
	require.NotNil(t, presence[0].Assigned)
	assert.Equal(t, bobID, presence[0].Assigned.ID)
	assert.True(t, presence[0].AssignedSwapped)
	assert.Contains(t, presence[0].AssignedSwapInfo, "Accepted swap:")
	assert.Contains(t, presence[0].AssignedSwapInfo, "Alice")
	assert.Contains(t, presence[0].AssignedSwapInfo, "Bob")
}

func TestLoadCurrentUserPresenceStatus_SetsHatDayAndLeave(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupPresenceTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	today := time.Now().Format("2006-01-02")
	tomorrow := testutil.NextBusinessDay(time.Now().AddDate(0, 0, 1)).Format("2006-01-02")
	_, err = db.CreateRotaAssignment(ctx, today, aliceID, false, nil)
	require.NoError(t, err)
	_, err = db.CreateRotaAssignment(ctx, tomorrow, aliceID, false, nil)
	require.NoError(t, err)

	_, err = db.CreateLeaveRecord(ctx, aliceID, today, today)
	require.NoError(t, err)
	_, err = db.CreateWFHRequest(ctx, aliceID, tomorrow)
	require.NoError(t, err)

	handler := &Handler{db: db}
	data := map[string]any{}

	handler.loadCurrentUserPresenceStatus(ctx, data, "alice@example.com")

	assert.Equal(t, currentUserStatusOnLeave, data["CurrentUserPresenceStatus"])
	// On leave: HAT day badge is hidden, and the Next HAT day skips
	// the leave-reassigned day in favor of the next post-leave HAT.
	_, hasHAT := data["CurrentUserHasHATDay"]
	assert.False(t, hasHAT, "CurrentUserHasHATDay should not be set when on leave")
	assert.Equal(t, tomorrow, data["CurrentUserNextHATDay"])
	assert.Equal(t, tomorrow, data["CurrentUserNextWFHDay"])
	assert.Equal(t, today, data["CurrentUserNextLeaveDay"])
}

func TestLoadCurrentUserPresenceStatus_CoverDutyIsNextHAT(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupPresenceTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	bobOriginalDate := testutil.NextBusinessDay(time.Now().AddDate(0, 0, 7)).Format("2006-01-02")
	aliceOwnHATDate := testutil.NextBusinessDay(time.Now().AddDate(0, 0, 9)).Format("2006-01-02")
	coverDate := testutil.NextBusinessDay(time.Now().AddDate(0, 0, 2)).Format("2006-01-02")

	bobAssignmentID, err := db.CreateRotaAssignment(ctx, bobOriginalDate, bobID, false, nil)
	require.NoError(t, err)
	_, err = db.CreateRotaAssignment(ctx, aliceOwnHATDate, aliceID, false, nil)
	require.NoError(t, err)

	// Alice is the cover for Bob on the near future date.
	_, err = db.CreateRotaAssignment(ctx, coverDate, aliceID, true, &bobAssignmentID)
	require.NoError(t, err)

	handler := &Handler{db: db}
	data := map[string]any{}

	handler.loadCurrentUserPresenceStatus(ctx, data, "alice@example.com")

	// The cover duty comes first chronologically. A cover is on HAT
	// duty - just for someone else - so the cover date is the Next HAT.
	assert.Equal(t, coverDate, data["CurrentUserNextHATDay"])
}

func TestLoadCurrentUserPresenceStatus_CoverDutyToday_ShowsHATBadge(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupPresenceTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	// Bob's primary HAT day is today; Alice is the cover for Bob today.
	today := time.Now().Format("2006-01-02")
	bobAssignmentID, err := db.CreateRotaAssignment(ctx, today, bobID, false, nil)
	require.NoError(t, err)
	_, err = db.CreateRotaAssignment(ctx, today, aliceID, true, &bobAssignmentID)
	require.NoError(t, err)

	handler := &Handler{db: db}
	data := map[string]any{}

	handler.loadCurrentUserPresenceStatus(ctx, data, "alice@example.com")

	// Alice is the cover today, so she has HAT status. The badge must
	// be shown - cover duty is HAT duty, just on behalf of someone else.
	assert.Equal(t, true, data["CurrentUserHasHATDay"])
	assert.Equal(t, today, data["CurrentUserNextHATDay"])
}

func TestLoadCurrentUserPresenceStatus_CoverDutyToday_OnLeave_StillHidesBadge(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupPresenceTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	// Bob's primary HAT day is today; Alice is the cover for Bob today,
	// and Alice is also on leave today. The cover assignment is stale
	// data in this case - the user cannot actually cover if they are on
	// leave - so the HAT badge is hidden and the status is "On leave".
	today := time.Now().Format("2006-01-02")
	bobAssignmentID, err := db.CreateRotaAssignment(ctx, today, bobID, false, nil)
	require.NoError(t, err)
	_, err = db.CreateRotaAssignment(ctx, today, aliceID, true, &bobAssignmentID)
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, aliceID, today, today)
	require.NoError(t, err)

	handler := &Handler{db: db}
	data := map[string]any{}

	handler.loadCurrentUserPresenceStatus(ctx, data, "alice@example.com")

	assert.Equal(t, currentUserStatusOnLeave, data["CurrentUserPresenceStatus"])
	_, hasHAT := data["CurrentUserHasHATDay"]
	assert.False(t, hasHAT, "HAT badge must be hidden when the user is on leave, even with a cover assignment")
	assert.Equal(t, today, data["CurrentUserNextLeaveDay"])
}

func TestLoadCurrentUserPresenceStatus_RejectedLeave_DoesNotHideHAT(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupPresenceTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	// Alice has a HAT day in the future and a leave for that day, but
	// the leave has been rejected - the reassignment was rolled back and
	// the original HAT day stands.
	hatDay := testutil.NextBusinessDay(time.Now().AddDate(0, 0, 1)).Format("2006-01-02")
	_, err = db.CreateRotaAssignment(ctx, hatDay, aliceID, false, nil)
	require.NoError(t, err)
	leaveID, err := db.CreateLeaveRecord(ctx, aliceID, hatDay, hatDay)
	require.NoError(t, err)
	require.NoError(t, db.UpdateLeaveStatus(ctx, leaveID, "rejected"))

	handler := &Handler{db: db}
	data := map[string]any{}

	handler.loadCurrentUserPresenceStatus(ctx, data, "alice@example.com")

	// The rejected leave is not an active leave, so the HAT day stands.
	assert.Equal(t, hatDay, data["CurrentUserNextHATDay"])
	// And the next leave should be empty - a rejected leave with a future
	// date is not surfaced as an upcoming leave.
	_, hasLeave := data["CurrentUserNextLeaveDay"]
	assert.False(t, hasLeave, "rejected leave with future date should not be surfaced as upcoming leave")
}

func TestLoadCurrentUserPresenceStatus_AllLeaveDays_NoNextHAT(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupPresenceTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	// Two upcoming HAT days, both entirely covered by leave.
	firstHAT := testutil.NextBusinessDay(time.Now().AddDate(0, 0, 1)).Format("2006-01-02")
	lastHAT := testutil.NextBusinessDay(time.Now().AddDate(0, 0, 2)).Format("2006-01-02")
	_, err = db.CreateRotaAssignment(ctx, firstHAT, aliceID, false, nil)
	require.NoError(t, err)
	_, err = db.CreateRotaAssignment(ctx, lastHAT, aliceID, false, nil)
	require.NoError(t, err)

	// Leave spans from the first HAT day through the last.
	_, err = db.CreateLeaveRecord(ctx, aliceID, firstHAT, lastHAT)
	require.NoError(t, err)

	handler := &Handler{db: db}
	data := map[string]any{}

	handler.loadCurrentUserPresenceStatus(ctx, data, "alice@example.com")

	_, hasNext := data["CurrentUserNextHATDay"]
	assert.False(t, hasNext, "Next HAT should not be set when every HAT day is on leave")
	assert.Equal(t, firstHAT, data["CurrentUserNextLeaveDay"])
}

func TestLoadCurrentUserPresenceStatus_SwapReflectedInNextHAT(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupPresenceTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	aliceOriginal := testutil.NextBusinessDay(time.Now().AddDate(0, 0, 3)).Format("2006-01-02")
	bobOriginal := testutil.NextBusinessDay(time.Now().AddDate(0, 0, 7)).Format("2006-01-02")

	aliceAssignmentID, err := db.CreateRotaAssignment(ctx, aliceOriginal, aliceID, false, nil)
	require.NoError(t, err)
	bobAssignmentID, err := db.CreateRotaAssignment(ctx, bobOriginal, bobID, false, nil)
	require.NoError(t, err)

	swapID, err := db.CreateHatSwap(ctx, aliceAssignmentID, bobAssignmentID, aliceID, bobID)
	require.NoError(t, err)
	require.NoError(t, db.ExecuteSwap(ctx, swapID))

	handler := &Handler{db: db}
	data := map[string]any{}

	handler.loadCurrentUserPresenceStatus(ctx, data, "alice@example.com")

	// After the swap Alice's member_id is on Bob's original day, so the
	// Status card must surface the post-swap date, not her pre-swap one.
	assert.Equal(t, bobOriginal, data["CurrentUserNextHATDay"])
	assert.NotEqual(t, aliceOriginal, data["CurrentUserNextHATDay"])
}

func TestLoadCurrentUserPresenceStatus_NextWFHUsesEarliestUpcomingDate(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupPresenceTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	nearDate := testutil.NextBusinessDay(time.Now().AddDate(0, 0, 1)).Format("2006-01-02")
	farDate := testutil.NextBusinessDay(time.Now().AddDate(0, 0, 7)).Format("2006-01-02")

	_, err = db.CreateWFHRequest(ctx, aliceID, nearDate)
	require.NoError(t, err)
	_, err = db.CreateWFHRequest(ctx, aliceID, farDate)
	require.NoError(t, err)

	handler := &Handler{db: db}
	data := map[string]any{}

	handler.loadCurrentUserPresenceStatus(ctx, data, "alice@example.com")

	assert.Equal(t, nearDate, data["CurrentUserNextWFHDay"])
}

func TestLoadCurrentUserPresenceStatus_NextWFHUpdatesAfterSettlement(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupPresenceTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	nearDate := testutil.NextBusinessDay(time.Now().AddDate(0, 0, 1)).Format("2006-01-02")
	farDate := testutil.NextBusinessDay(time.Now().AddDate(0, 0, 7)).Format("2006-01-02")

	_, err = db.CreateWFHRequest(ctx, aliceID, nearDate)
	require.NoError(t, err)
	_, err = db.CreateWFHRequest(ctx, aliceID, farDate)
	require.NoError(t, err)

	requests, err := db.GetWFHRequestsByMember(ctx, aliceID)
	require.NoError(t, err)

	var nearRequestID string
	for i := range requests {
		if requests[i].Date == nearDate {
			nearRequestID = requests[i].ID
			break
		}
	}
	require.NotEmpty(t, nearRequestID)

	require.NoError(t, db.UpdateWFHRequestStatus(ctx, nearRequestID, database.WFHStatusDenied))

	handler := &Handler{db: db}
	data := map[string]any{}

	handler.loadCurrentUserPresenceStatus(ctx, data, "alice@example.com")

	assert.Equal(t, farDate, data["CurrentUserNextWFHDay"])
}

func TestLoadCurrentUserPresenceStatus_RecurringWFH(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupPresenceTestDB(t)
	defer cleanup()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	require.NoError(t, db.SetTeamMemberPermanentWFH(ctx, aliceID, true))

	today := time.Now().Format("2006-01-02")
	_, err = db.CreateRotaAssignment(ctx, today, aliceID, false, nil)
	require.NoError(t, err)

	handler := &Handler{db: db}
	data := map[string]any{}

	handler.loadCurrentUserPresenceStatus(ctx, data, "alice@example.com")

	now := time.Now()
	if now.Weekday() >= time.Monday && now.Weekday() <= time.Friday {
		assert.Equal(t, currentUserStatusWFH, data["CurrentUserPresenceStatus"])
	} else {
		assert.Equal(t, currentUserStatusOnSite, data["CurrentUserPresenceStatus"])
	}
	assert.Equal(t, true, data["CurrentUserHasHATDay"])
}
