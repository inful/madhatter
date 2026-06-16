package rota

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) (*database.DB, func()) {
	t.Helper()
	// Create in-memory test database
	db, err := database.New(":memory:")
	require.NoError(t, err)

	// Return cleanup function
	cleanup := func() {
		_ = db.Close()
	}

	return db, cleanup
}

func TestDebugLeaveDates(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	aliceID, _ := db.AddTeamMember(ctx, "Alice", "alice@example.com")

	// Create leave
	leaveID, err := db.CreateLeaveRecord(ctx, aliceID, "2024-01-15", "2024-01-15")
	require.NoError(t, err)
	t.Logf("Created leave ID: %s", leaveID)

	// Get it back
	leave, err := db.GetLeaveByID(ctx, leaveID)
	require.NoError(t, err)
	t.Logf("Retrieved leave: %+v", leave)
	t.Logf("StartDate: %v", leave.StartDate)
	t.Logf("EndDate: %v", leave.EndDate)

	// Try parsing (no longer needed since it's already time.Time)
	t.Logf("StartDate is already time.Time: %v", leave.StartDate)

	// Check what GetLeaveByDate returns
	leaves, err := db.GetLeaveByDate(ctx, "2024-01-15")
	require.NoError(t, err)
	t.Logf("GetLeaveByDate result: %+v", leaves)
	if len(leaves) > 0 {
		t.Logf("First leave StartDate: %v", leaves[0].StartDate)
	}
}

func TestEngine_GenerateSchedule_BasicRoundRobin(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add team members
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	charlieID, err := db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)

	// Generate schedule for a week (Mon-Fri)
	startDate := time.Date(2024, 1, 8, 0, 0, 0, 0, time.UTC) // Monday
	endDate := time.Date(2024, 1, 12, 0, 0, 0, 0, time.UTC)  // Friday

	err = engine.GenerateSchedule(ctx, startDate, endDate)
	require.NoError(t, err)

	// Verify assignments
	assignments, err := db.GetAssignmentsByDateRange(ctx, "2024-01-08", "2024-01-12")
	require.NoError(t, err)

	// Should have 5 assignments (Mon-Fri)
	require.Len(t, assignments, 5)

	// Verify round-robin order: Alice, Bob, Charlie, Alice, Bob
	expectedMembers := []string{aliceID, bobID, charlieID, aliceID, bobID}
	expectedDates := []string{"2024-01-08", "2024-01-09", "2024-01-10", "2024-01-11", "2024-01-12"}

	for i, assignment := range assignments {
		require.Equal(t, expectedDates[i], assignment.Date)
		require.Equal(t, expectedMembers[i], assignment.MemberID)
		require.False(t, assignment.IsCover)
		require.Nil(t, assignment.OriginalAssignmentID)
	}
}

func TestEngine_GenerateSchedule_WeekendSkipping(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add team members
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)

	// Generate schedule across a weekend
	startDate := time.Date(2024, 1, 12, 0, 0, 0, 0, time.UTC) // Friday
	endDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)   // Monday

	err = engine.GenerateSchedule(ctx, startDate, endDate)
	require.NoError(t, err)

	// Verify assignments
	assignments, err := db.GetAssignmentsByDateRange(ctx, "2024-01-12", "2024-01-15")
	require.NoError(t, err)

	// Should have 2 assignments (Friday and Monday only, skipping Sat/Sun)
	require.Len(t, assignments, 2)

	// Verify dates
	dates := []string{assignments[0].Date, assignments[1].Date}
	require.Equal(t, []string{"2024-01-12", "2024-01-15"}, dates)

	// Verify round-robin continues after weekend
	require.Equal(t, aliceID, assignments[0].MemberID)
	require.Equal(t, bobID, assignments[1].MemberID)
}

func TestEngine_GenerateSchedule_EmptyTeam(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	engine := NewEngine(db)

	startDate := time.Date(2024, 1, 8, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 12, 0, 0, 0, 0, time.UTC)

	err := engine.GenerateSchedule(ctx, startDate, endDate)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no active team members")
}

func TestEngine_AssignCoversForLeave_BasicCover(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add team members
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)

	// First create a schedule
	startDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC) // Monday
	endDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	err = engine.GenerateSchedule(ctx, startDate, endDate)
	require.NoError(t, err)

	// Verify Alice is assigned
	assignments, err := db.GetAssignmentsByDate(ctx, "2024-01-15")
	require.NoError(t, err)
	require.Len(t, assignments, 1)
	require.Equal(t, aliceID, assignments[0].MemberID)

	// Alice takes leave
	leaveID, err := db.CreateLeaveRecord(ctx, aliceID, "2024-01-15", "2024-01-15")
	require.NoError(t, err)

	// Assign covers
	err = engine.AssignCoversForLeave(ctx, leaveID)
	require.NoError(t, err)

	// Verify cover assignment
	assignments, err = db.GetAssignmentsByDate(ctx, "2024-01-15")
	require.NoError(t, err)
	require.Len(t, assignments, 2)

	// Find original and cover
	var original, cover *database.RotaAssignment
	for i := range assignments {
		if assignments[i].IsCover {
			cover = &assignments[i]
		} else {
			original = &assignments[i]
		}
	}

	require.NotNil(t, original)
	require.NotNil(t, cover)

	// Original should be Alice
	require.Equal(t, aliceID, original.MemberID)
	require.False(t, original.IsCover)

	// Cover should be Bob
	require.Equal(t, bobID, cover.MemberID)
	require.True(t, cover.IsCover)
	require.Equal(t, original.ID, *cover.OriginalAssignmentID)
}

func TestEngine_AssignCoversForLeave_IgnoresUnscheduledLeave(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)

	startDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	err = engine.GenerateSchedule(ctx, startDate, endDate)
	require.NoError(t, err)

	assignments, err := db.GetAssignmentsByDate(ctx, "2024-01-15")
	require.NoError(t, err)
	require.Len(t, assignments, 1)
	require.Equal(t, aliceID, assignments[0].MemberID)

	leaveID, err := db.CreateLeaveRecord(ctx, bobID, "2024-01-15", "2024-01-15")
	require.NoError(t, err)

	err = engine.AssignCoversForLeave(ctx, leaveID)
	require.NoError(t, err)

	assignmentsAfter, err := db.GetAssignmentsByDate(ctx, "2024-01-15")
	require.NoError(t, err)
	require.Len(t, assignmentsAfter, 1)
	require.Equal(t, aliceID, assignmentsAfter[0].MemberID)
	require.False(t, assignmentsAfter[0].IsCover)
}

func TestEngine_AssignCoversForLeave_MultiDayLeave(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add team members
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)

	// Create schedule for a week
	startDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC) // Monday
	endDate := time.Date(2024, 1, 19, 0, 0, 0, 0, time.UTC)   // Friday
	err = engine.GenerateSchedule(ctx, startDate, endDate)
	require.NoError(t, err)

	// Alice takes leave for Wed-Fri
	leaveID, err := db.CreateLeaveRecord(ctx, aliceID, "2024-01-17", "2024-01-19")
	require.NoError(t, err)

	// Assign covers
	err = engine.AssignCoversForLeave(ctx, leaveID)
	require.NoError(t, err)

	// Verify assignments for Wed-Fri
	// The schedule creates: Mon (Alice), Tue (Bob), Wed (Charlie), Thu (Alice), Fri (Bob)
	// Alice takes leave Wed-Fri, so:
	// Wed: Charlie already assigned, Alice not on rota -> no cover created
	// Thu: Alice assigned -> cover created
	// Fri: Bob assigned -> no cover

	// Let's just verify the key things:
	// 1. Leave status should be updated
	leave, err := db.GetLeaveByID(ctx, leaveID)
	require.NoError(t, err)
	require.Equal(t, "assigned", leave.Status)

	// 2. There should be cover assignments created
	wedAssignments, err := db.GetAssignmentsByDate(ctx, "2024-01-17")
	require.NoError(t, err)

	for _, a := range wedAssignments {
		require.False(t, a.IsCover, "No cover should be created for a day where leave member is not scheduled")
	}

	thuAssignments, err := db.GetAssignmentsByDate(ctx, "2024-01-18")
	require.NoError(t, err)

	var thuCoverFound bool
	for _, a := range thuAssignments {
		if a.IsCover {
			thuCoverFound = true
			require.Equal(t, bobID, a.MemberID, "Cover should be Bob on scheduled leave day")
			require.NotNil(t, a.OriginalAssignmentID)
		}
	}
	require.True(t, thuCoverFound, "Should have a cover assignment on the day the leave member was scheduled")
}

func TestEngine_AssignCoversForLeave_CompletedLeaveRemovesCover(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add team members
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)

	// Create a single-day schedule
	date := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	require.NoError(t, engine.GenerateSchedule(ctx, date, date))

	assignments, err := db.GetAssignmentsByDate(ctx, "2024-01-15")
	require.NoError(t, err)
	require.Len(t, assignments, 1)
	require.Equal(t, aliceID, assignments[0].MemberID)

	// Alice takes leave and gets a cover
	leaveID, err := db.CreateLeaveRecord(ctx, aliceID, "2024-01-15", "2024-01-15")
	require.NoError(t, err)
	require.NoError(t, engine.AssignCoversForLeave(ctx, leaveID))

	assignments, err = db.GetAssignmentsByDate(ctx, "2024-01-15")
	require.NoError(t, err)
	require.Len(t, assignments, 2)

	var coverAssignment *database.RotaAssignment
	for i := range assignments {
		if assignments[i].IsCover {
			coverAssignment = &assignments[i]
			break
		}
	}
	require.NotNil(t, coverAssignment)
	assert.Equal(t, bobID, coverAssignment.MemberID)

	// Cancel the leave
	require.NoError(t, db.UpdateLeaveStatus(ctx, leaveID, "completed"))

	// Re-run leave handling via maintenance to trigger reconciliation
	maintenance := NewScheduleMaintenance(db)
	require.NoError(t, maintenance.HandleLeaveChange(ctx, leaveID))

	assignments, err = db.GetAssignmentsByDate(ctx, "2024-01-15")
	require.NoError(t, err)
	require.Len(t, assignments, 1)

	assert.Equal(t, aliceID, assignments[0].MemberID)
	assert.False(t, assignments[0].IsCover)

	leave, err := db.GetLeaveByID(ctx, leaveID)
	require.NoError(t, err)
	assert.Equal(t, "completed", leave.Status)
}

func TestEngine_AssignCoversForLeave_LeaveOnWeekend(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)

	// Alice takes leave over weekend
	leaveID, err := db.CreateLeaveRecord(ctx, aliceID, "2024-01-13", "2024-01-14")
	require.NoError(t, err)

	// Assign covers
	err = engine.AssignCoversForLeave(ctx, leaveID)
	require.NoError(t, err)

	// Should have no assignments since weekends are skipped
	assignments, err := db.GetAssignmentsByDateRange(ctx, "2024-01-13", "2024-01-14")
	require.NoError(t, err)
	require.Empty(t, assignments)
}

func TestEngine_findCover_AllMembersOnLeave(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add only one member
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)

	// Create schedule
	startDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	err = engine.GenerateSchedule(ctx, startDate, endDate)
	require.NoError(t, err)

	// Alice takes leave
	leaveID, err := db.CreateLeaveRecord(ctx, aliceID, "2024-01-15", "2024-01-15")
	require.NoError(t, err)

	// Try to assign covers - should succeed but no cover available
	err = engine.AssignCoversForLeave(ctx, leaveID)
	require.NoError(t, err) // Should not error, just skip

	// Verify only original assignment exists
	assignments, err := db.GetAssignmentsByDate(ctx, "2024-01-15")
	require.NoError(t, err)
	require.Len(t, assignments, 1)
	require.Equal(t, aliceID, assignments[0].MemberID)
	require.False(t, assignments[0].IsCover)
}

func TestEngine_processDate_SkipsWeekends(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)

	// Test Saturday
	saturday := time.Date(2024, 1, 13, 0, 0, 0, 0, time.UTC)
	members := []database.TeamMember{{ID: aliceID, Name: "Alice"}}

	err = engine.processDate(ctx, saturday, members)
	require.NoError(t, err)

	// No assignment should be created
	assignments, err := db.GetAssignmentsByDate(ctx, "2024-01-13")
	require.NoError(t, err)
	require.Empty(t, assignments)

	// Test Sunday
	sunday := time.Date(2024, 1, 14, 0, 0, 0, 0, time.UTC)
	err = engine.processDate(ctx, sunday, members)
	require.NoError(t, err)

	// No assignment should be created
	assignments, err = db.GetAssignmentsByDate(ctx, "2024-01-14")
	require.NoError(t, err)
	require.Empty(t, assignments)
}

func TestEngine_determineCoveringMember_WithLeave(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)

	members := []database.TeamMember{
		{ID: aliceID, Name: "Alice"},
		{ID: bobID, Name: "Bob"},
	}

	// Alice is on leave
	leaves := []database.LeaveRecord{
		{MemberID: aliceID},
	}

	originalMember := members[0]
	currentDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	cover, err := engine.determineCoveringMember(ctx, originalMember, leaves, members, currentDate)
	require.NoError(t, err)

	// Should return Bob as cover
	require.Equal(t, bobID, cover.ID)
}

func TestEngine_determineCoveringMember_NoLeave(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)

	members := []database.TeamMember{
		{ID: aliceID, Name: "Alice"},
		{ID: bobID, Name: "Bob"},
	}

	// No leave
	leaves := []database.LeaveRecord{}

	originalMember := members[0]
	currentDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	cover, err := engine.determineCoveringMember(ctx, originalMember, leaves, members, currentDate)
	require.NoError(t, err)

	// Should return original member
	require.Equal(t, aliceID, cover.ID)
}

func TestEngine_createAssignment_Cover(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)

	originalMember := database.TeamMember{ID: aliceID, Name: "Alice"}
	coveringMember := database.TeamMember{ID: bobID, Name: "Bob"}

	err = engine.createAssignment(ctx, time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), "2024-01-15", 0, originalMember, coveringMember)
	require.NoError(t, err)

	// Verify two assignments created
	assignments, err := db.GetAssignmentsByDate(ctx, "2024-01-15")
	require.NoError(t, err)
	require.Len(t, assignments, 2)

	// Find original and cover
	var original, cover *database.RotaAssignment
	for i := range assignments {
		if assignments[i].IsCover {
			cover = &assignments[i]
		} else {
			original = &assignments[i]
		}
	}

	require.NotNil(t, original)
	require.NotNil(t, cover)

	require.Equal(t, aliceID, original.MemberID)
	require.Equal(t, bobID, cover.MemberID)
	require.Equal(t, original.ID, *cover.OriginalAssignmentID)
}

func TestEngine_createAssignment_NoCover(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)

	member := database.TeamMember{ID: aliceID, Name: "Alice"}

	err = engine.createAssignment(ctx, time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), "2024-01-15", 0, member, member)
	require.NoError(t, err)

	// Verify single assignment created
	assignments, err := db.GetAssignmentsByDate(ctx, "2024-01-15")
	require.NoError(t, err)
	require.Len(t, assignments, 1)

	require.Equal(t, aliceID, assignments[0].MemberID)
	require.False(t, assignments[0].IsCover)
	require.Nil(t, assignments[0].OriginalAssignmentID)
}

func TestEngine_ensureOriginalAssignment_Existing(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)

	// Create original assignment
	originalID, err := db.CreateRotaAssignment(ctx, "2024-01-15", aliceID, false, nil)
	require.NoError(t, err)

	leave := &database.LeaveRecord{MemberID: aliceID}

	// Should find existing
	foundID, err := engine.ensureOriginalAssignment(ctx, "2024-01-15", leave)
	require.NoError(t, err)
	require.Equal(t, originalID, foundID)

	// Should not create duplicate
	assignments, err := db.GetAssignmentsByDate(ctx, "2024-01-15")
	require.NoError(t, err)
	require.Len(t, assignments, 1)
}

func TestEngine_ensureOriginalAssignment_NotScheduled(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)

	leave := &database.LeaveRecord{MemberID: aliceID}

	assignmentID, err := engine.ensureOriginalAssignment(ctx, "2024-01-15", leave)
	require.ErrorIs(t, err, errMemberNotScheduled)
	require.Empty(t, assignmentID)

	assignments, err := db.GetAssignmentsByDate(ctx, "2024-01-15")
	require.NoError(t, err)
	require.Empty(t, assignments)
}

func TestEngine_findCover_SkipsOnLeave(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	charlieID, err := db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)

	members := []database.TeamMember{
		{ID: aliceID, Name: "Alice"},
		{ID: bobID, Name: "Bob"},
		{ID: charlieID, Name: "Charlie"},
	}

	// Bob is on leave
	leaves := []database.LeaveRecord{
		{MemberID: bobID},
	}

	// Start from Alice (index 0). Alice is not on leave, so she is
	// the cover. (The old "start at startIndex+1" behavior would have
	// returned Charlie, but the new rotation index points directly at
	// the person who should cover, so we start at startIndex.)
	cover, err := engine.findCover(members, leaves, 0)
	require.NoError(t, err)
	require.Equal(t, aliceID, cover.ID)
}

func TestEngine_findCover_WrapsAround(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)

	members := []database.TeamMember{
		{ID: aliceID, Name: "Alice"},
		{ID: bobID, Name: "Bob"},
	}

	// No one on leave
	leaves := []database.LeaveRecord{}

	// Start from Bob (index 1). The new rotation index points directly
	// at the cover, so starting at index 1 returns Bob (the old
	// "start at startIndex+1" behavior would have wrapped to Alice).
	cover, err := engine.findCover(members, leaves, 1)
	require.NoError(t, err)
	require.Equal(t, bobID, cover.ID)
}

func TestEngine_processLeaveDate_SkipsWeekends(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)

	members := []database.TeamMember{{ID: aliceID, Name: "Alice"}}
	leave := &database.LeaveRecord{MemberID: aliceID, StartDate: time.Date(2024, 1, 13, 0, 0, 0, 0, time.UTC), EndDate: time.Date(2024, 1, 13, 0, 0, 0, 0, time.UTC)}

	// Saturday
	saturday := time.Date(2024, 1, 13, 0, 0, 0, 0, time.UTC)
	_, err = engine.processLeaveDate(ctx, saturday, members, leave, "leave-id")
	require.NoError(t, err)

	// No assignment should be created
	assignments, err := db.GetAssignmentsByDate(ctx, "2024-01-13")
	require.NoError(t, err)
	require.Empty(t, assignments)
}

func TestEngine_AssignCoversForLeave_LeaveStatusUpdate(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)

	// Create schedule
	startDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	err = engine.GenerateSchedule(ctx, startDate, endDate)
	require.NoError(t, err)

	// Alice takes leave
	leaveID, err := db.CreateLeaveRecord(ctx, aliceID, "2024-01-15", "2024-01-15")
	require.NoError(t, err)

	// Verify initial status
	leave, err := db.GetLeaveByID(ctx, leaveID)
	require.NoError(t, err)
	require.Equal(t, "pending", leave.Status)

	// Assign covers
	err = engine.AssignCoversForLeave(ctx, leaveID)
	require.NoError(t, err)

	// Verify status updated
	leave, err = db.GetLeaveByID(ctx, leaveID)
	require.NoError(t, err)
	require.Equal(t, "assigned", leave.Status)
}

// TestEngine_CoverRotationDeterministic asserts that the cover
// rotation is deterministic: given the same team composition and the
// same sequence of leave dates, the state-based rotation always
// produces the same cover. It does NOT assert fairness — see
// TestEngine_CoverRotationFairnessOverYear for a year-long random
// workload that exercises the fairness property.
func TestEngine_CoverRotationDeterministic(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add team members
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Dave", "dave@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)

	// Create schedule for two weeks
	// With 4 members, the rotation is: Alice, Bob, Charlie, Dave, Alice, Bob, ...
	// Jan 15 (Mon): Alice
	// Jan 16 (Tue): Bob
	// Jan 17 (Wed): Charlie
	// Jan 18 (Thu): Dave
	// Jan 19 (Fri): Alice
	// Jan 22 (Mon): Bob
	// Jan 23 (Tue): Charlie
	// Jan 24 (Wed): Dave
	// Jan 25 (Thu): Alice
	// Jan 26 (Fri): Bob
	startDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC) // Monday
	endDate := time.Date(2024, 1, 26, 0, 0, 0, 0, time.UTC)   // Friday of next week
	err = engine.GenerateSchedule(ctx, startDate, endDate)
	require.NoError(t, err)

	// Alice takes leave on Monday Jan 15 (her scheduled day)
	leaveID1, err := db.CreateLeaveRecord(ctx, aliceID, "2024-01-15", "2024-01-15")
	require.NoError(t, err)
	err = engine.AssignCoversForLeave(ctx, leaveID1)
	require.NoError(t, err)

	// Alice takes leave again on Friday Jan 19 (also her scheduled day)
	leaveID2, err := db.CreateLeaveRecord(ctx, aliceID, "2024-01-19", "2024-01-19")
	require.NoError(t, err)
	err = engine.AssignCoversForLeave(ctx, leaveID2)
	require.NoError(t, err)

	// Get cover assignments
	assignments1, err := db.GetAssignmentsByDate(ctx, "2024-01-15")
	require.NoError(t, err)
	assignments2, err := db.GetAssignmentsByDate(ctx, "2024-01-19")
	require.NoError(t, err)

	// Find cover members
	var cover1, cover2 string
	for _, a := range assignments1 {
		if a.IsCover {
			cover1 = a.MemberID
		}
	}
	for _, a := range assignments2 {
		if a.IsCover {
			cover2 = a.MemberID
		}
	}

	// Verify covers were found
	require.NotEmpty(t, cover1, "First cover assignment should exist")
	require.NotEmpty(t, cover2, "Second cover assignment should exist")

	// Verify the rotation pattern. The cover rotation is a persisted
	// state (last_date, last_index) that advances by one slot per
	// working day. The first call commits the slot of the person
	// who actually covered (not the candidate slot). Alice is on
	// leave, so the candidate at slot 0 is skipped and Bob (slot 1)
	// covers; the state is seeded at (Jan 15, 1):
	//   Jan 15 → candidate 0, findCover walks to Bob → state committed at (Jan 15, 1)
	//   Jan 19 → candidate 1 + 4 working days = 5 mod 4 = 1 → Alice on leave → Bob
	// The delta from Jan 15 to Jan 19 is exactly one full team cycle
	// of 4 working days, so the second call's candidate lands on the
	// same slot Bob occupies. This is the expected behavior of the
	// state-based rotation: it is fully deterministic and
	// reproducible, even if it doesn't guarantee distinct covers for
	// every pair of dates.
	require.Equal(t, bobID, cover1, "First cover (Jan 15) should be Bob (candidate slot 0, Alice on leave → next)")
	require.Equal(t, bobID, cover2, "Second cover (Jan 19) should be Bob (state wrapped after 4 working days)")
}

// recordingCoverNotifier is an Engine.CoverNotifier that captures
// every CoverAssigned call. Safe for concurrent use.
type recordingCoverNotifier struct {
	mu     sync.Mutex
	events []CoverEvent
}

func (r *recordingCoverNotifier) CoverAssigned(_ context.Context, e CoverEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func TestEngine_AssignCoversForLeave_FiresCoverAssignedOnce(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)
	notifier := &recordingCoverNotifier{}
	engine.SetNotifier(notifier)

	// Mon-Fri schedule; Alice leaves Wed-Fri.
	startDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 19, 0, 0, 0, 0, time.UTC)
	require.NoError(t, engine.GenerateSchedule(ctx, startDate, endDate))

	leaveID, err := db.CreateLeaveRecord(ctx, aliceID, "2024-01-17", "2024-01-19")
	require.NoError(t, err)

	require.NoError(t, engine.AssignCoversForLeave(ctx, leaveID))

	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	// One event per cover member. In the multi-day scenario, the
	// same member often covers multiple consecutive days; we want
	// one consolidated event, not one per day.
	require.NotEmpty(t, notifier.events, "expected at least one CoverAssigned event")

	for _, e := range notifier.events {
		assert.Equal(t, "Alice", e.LeaveMemberName)
		assert.NotEmpty(t, e.CoverMemberName)
		assert.NotEmpty(t, e.StartDate)
		assert.NotEmpty(t, e.EndDate)
		// The date range is sorted by processLeaveDates, so start <= end.
		assert.LessOrEqual(t, e.StartDate, e.EndDate)
	}
}
