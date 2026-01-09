package rota

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestEngine_MultipleSeparateLeaves tests that cover assignments are fair
// across multiple separate leave instances (not multi-day leaves).
func TestEngine_MultipleSeparateLeaves(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add 5 team members
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	charlieID, err := db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Dave", "dave@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Eve", "eve@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)

	// Create schedule for January 2024 (multiple weeks)
	// Week 1: Alice(Mon), Bob(Tue), Charlie(Wed), Dave(Thu), Eve(Fri)
	// Week 2: Alice(Mon), Bob(Tue), Charlie(Wed), Dave(Thu), Eve(Fri)
	// Week 3: Alice(Mon), Bob(Tue), Charlie(Wed), Dave(Thu), Eve(Fri)
	startDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC) // Monday
	endDate := time.Date(2024, 1, 26, 0, 0, 0, 0, time.UTC)   // Friday 2 weeks later
	err = engine.GenerateSchedule(ctx, startDate, endDate)
	require.NoError(t, err)

	// Scenario: Bob takes leave on 3 separate occasions throughout the schedule
	// Create leaves in chronological order to test proper cover rotation

	// Leave 1: Tuesday Jan 16 (Bob's scheduled day)
	leave1ID, err := db.CreateLeaveRecord(ctx, bobID, "sick", "2024-01-16", "2024-01-16")
	require.NoError(t, err)
	err = engine.AssignCoversForLeave(ctx, leave1ID)
	require.NoError(t, err)

	// Leave 2: Wednesday Jan 17 (Charlie's scheduled day) - happens AFTER Bob's first leave
	leave2ID, err := db.CreateLeaveRecord(ctx, charlieID, "sick", "2024-01-17", "2024-01-17")
	require.NoError(t, err)
	err = engine.AssignCoversForLeave(ctx, leave2ID)
	require.NoError(t, err)

	// Leave 3: Tuesday Jan 23 (Bob's scheduled day again) - happens AFTER the above leaves
	leave3ID, err := db.CreateLeaveRecord(ctx, bobID, "sick", "2024-01-23", "2024-01-23")
	require.NoError(t, err)
	err = engine.AssignCoversForLeave(ctx, leave3ID)
	require.NoError(t, err)

	// Get team members to map IDs to names
	members, err := db.GetActiveTeamMembers(ctx)
	require.NoError(t, err)
	memberNames := make(map[string]string)
	for _, m := range members {
		memberNames[m.ID] = m.Name
	}

	// Get all cover assignments
	assignments1, err3 := db.GetAssignmentsByDate(ctx, "2024-01-16")
	require.NoError(t, err3)
	assignments3, err4 := db.GetAssignmentsByDate(ctx, "2024-01-17")
	require.NoError(t, err4)
	assignments4, err5 := db.GetAssignmentsByDate(ctx, "2024-01-23")
	require.NoError(t, err5)

	// Find cover members
	var cover1, cover2, cover3 string
	for _, a := range assignments1 {
		if a.IsCover {
			cover1 = a.MemberID
		}
	}
	for _, a := range assignments3 {
		if a.IsCover {
			cover2 = a.MemberID
		}
	}
	for _, a := range assignments4 {
		if a.IsCover {
			cover3 = a.MemberID
		}
	}

	t.Logf("Cover 1 (Jan 16, Bob out): %s", memberNames[cover1])
	t.Logf("Cover 2 (Jan 17, Charlie out): %s", memberNames[cover2])
	t.Logf("Cover 3 (Jan 23, Bob out again): %s", memberNames[cover3])

	// Expected fair rotation:
	// R1 (original): Alice, Bob, Charlie, Dave, Eve (repeats)
	// R2 (cover): Starts from beginning independently - Alice, Bob, Charlie, Dave, Eve (repeats)
	//
	// Jan 16 (Bob out): First cover needed, R2 starts from Alice
	// Jan 17 (Charlie out): R2 continues from Bob (next after Alice)
	// Jan 23 (Bob out): R2 continues from Charlie (next after Bob)

	// The key insight: R2 is completely independent from R1
	require.Equal(t, aliceID, cover1, "First cover (Jan 16) should be Alice (R2 starts from beginning)")
	require.Equal(t, bobID, cover2, "Second cover (Jan 17) should be Bob (next in R2 after Alice)")
	require.Equal(t, charlieID, cover3, "Third cover (Jan 23) should be Charlie (next in R2 after Bob)")

	// Test would fail if the same person is always chosen for cover
	// or if the rotation doesn't continue across separate leave instances
}

// TestEngine_CoverRotationAcrossMultipleMembers tests that when different people
// take leave, the cover rotation is fair and doesn't always pick the same person.
func TestEngine_CoverRotationAcrossMultipleMembers(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add 4 team members
	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	charlieID, err := db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)
	daveID, err := db.AddTeamMember(ctx, "Dave", "dave@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)

	// Create schedule for 2 weeks
	startDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC) // Monday
	endDate := time.Date(2024, 1, 26, 0, 0, 0, 0, time.UTC)   // Friday
	err = engine.GenerateSchedule(ctx, startDate, endDate)
	require.NoError(t, err)

	// Alice takes leave on Mon Jan 15 (her day)
	leaveAlice1, err := db.CreateLeaveRecord(ctx, aliceID, "sick", "2024-01-15", "2024-01-15")
	require.NoError(t, err)
	err = engine.AssignCoversForLeave(ctx, leaveAlice1)
	require.NoError(t, err)

	// Bob takes leave on Tue Jan 16 (his day)
	leaveBob1, err := db.CreateLeaveRecord(ctx, bobID, "sick", "2024-01-16", "2024-01-16")
	require.NoError(t, err)
	err = engine.AssignCoversForLeave(ctx, leaveBob1)
	require.NoError(t, err)

	// Charlie takes leave on Wed Jan 17 (his day)
	leaveCharlie1, err := db.CreateLeaveRecord(ctx, charlieID, "sick", "2024-01-17", "2024-01-17")
	require.NoError(t, err)
	err = engine.AssignCoversForLeave(ctx, leaveCharlie1)
	require.NoError(t, err)

	// Alice takes leave again on Mon Jan 22
	leaveAlice2, err := db.CreateLeaveRecord(ctx, aliceID, "sick", "2024-01-22", "2024-01-22")
	require.NoError(t, err)
	err = engine.AssignCoversForLeave(ctx, leaveAlice2)
	require.NoError(t, err)

	// Get all cover assignments
	covers := make(map[string]string) // date -> coverID
	memberNames := map[string]string{
		aliceID:   "Alice",
		bobID:     "Bob",
		charlieID: "Charlie",
		daveID:    "Dave",
	}

	for _, date := range []string{"2024-01-15", "2024-01-16", "2024-01-17", "2024-01-22"} {
		assignments, err := db.GetAssignmentsByDate(ctx, date)
		require.NoError(t, err)
		for _, a := range assignments {
			if a.IsCover {
				covers[date] = a.MemberID
			}
		}
	}

	t.Logf("Cover for Alice (Jan 15): %s", memberNames[covers["2024-01-15"]])
	t.Logf("Cover for Bob (Jan 16): %s", memberNames[covers["2024-01-16"]])
	t.Logf("Cover for Charlie (Jan 17): %s", memberNames[covers["2024-01-17"]])
	if cover, ok := covers["2024-01-22"]; ok {
		t.Logf("Cover for Alice (Jan 22): %s", memberNames[cover])
	} else {
		t.Log("No cover on Jan 22 because Alice was not scheduled")
	}

	// Expected fair rotation with independent R2 (team: Alice, Bob, Charlie, Dave):
	// Jan 15 (Alice out): R2 starts from 0, Alice on leave, skip to index 1 -> Bob
	// Jan 16 (Bob out): R2 at index 1 (Bob just covered), next is 2 -> Charlie
	// Jan 17 (Charlie out): R2 at index 2 (Charlie just covered), next is 3 -> Dave
	// Jan 22 (Alice out): Alice was not scheduled, so no cover is created
	//
	// With 4 people and 4 cover needs, someone must cover twice. The fair rotation means
	// each person covers in order before anyone covers a second time.

	require.Equal(t, bobID, covers["2024-01-15"], "Bob should cover (R2 starts, skip Alice who's on leave)")
	require.Equal(t, charlieID, covers["2024-01-16"], "Charlie should cover (next in R2)")
	require.Equal(t, daveID, covers["2024-01-17"], "Dave should cover (next in R2)")
	require.NotContains(t, covers, "2024-01-22", "No cover should be created when leave member was not scheduled")
}
