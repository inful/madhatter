package rota

import (
	"context"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/require"
)

// TestEngine_CoverRotationWithFutureCoverStuck is a regression test for a
// production bug where the R2 cover rotation was anchored on a cover from a
// FUTURE date, causing the same person to be picked for every new leave.
//
// Scenario: The schedule has been generated for a long period (e.g. 30 days).
// A cover was assigned for a future date. When new leaves are then added for
// earlier dates, the most-recent cover by date is the future one, so the
// rotation gets stuck and the same person covers every new leave.
//
// Under the current date-derived rotation (see Engine.coverRotationIndex)
// the bug cannot recur: the rotation is a pure function of the leave date
// and the team composition, with no DB-anchored state to be "stuck" on a
// future cover. This test is kept as a regression guard — if a future
// change re-introduces a DB-anchored rotation, this test will fail.
func TestEngine_CoverRotationWithFutureCoverStuck(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add team members.
	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	charlieID, err := db.AddTeamMember(ctx, "Charlie", "charlie@example.com")
	require.NoError(t, err)
	daveID, err := db.AddTeamMember(ctx, "Dave", "dave@example.com")
	require.NoError(t, err)

	engine := NewEngine(db)

	// Generate a schedule for two weeks (Jan 15-26, Mon-Fri).
	startDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 26, 0, 0, 0, 0, time.UTC)
	err = engine.GenerateSchedule(ctx, startDate, endDate)
	require.NoError(t, err)

	// Find the assignment IDs for the future date Jan 26 so we can plant a
	// cover record directly. The "most recent cover" in the DB will then be
	// this Alice cover (Jan 26 is the highest date in the table).
	assignmentsJan26, err := db.GetAssignmentsByDate(ctx, "2024-01-26")
	require.NoError(t, err)
	require.NotEmpty(t, assignmentsJan26)

	var originalIDJan26 string
	for _, a := range assignmentsJan26 {
		if !a.IsCover {
			originalIDJan26 = a.ID
		}
	}
	require.NotEmpty(t, originalIDJan26, "Should have an original assignment for Jan 26")

	// Plant the future cover: Alice covers Jan 26.
	_, err = db.CreateRotaAssignment(ctx, "2024-01-26", memberIDByName(t, db, "Alice"), true, &originalIDJan26)
	require.NoError(t, err)

	// Add a leave for Jan 16 (Bob's scheduled day). The planted future
	// cover on Jan 26 must not affect the cover chosen for Jan 16.
	leave1ID, err := db.CreateLeaveRecord(ctx, bobID, "2024-01-16", "2024-01-16")
	require.NoError(t, err)
	err = engine.AssignCoversForLeave(ctx, leave1ID)
	require.NoError(t, err)

	// Add a second leave for Jan 17 (Charlie's day). The future cover on
	// Jan 26 must not anchor the rotation here either.
	leave2ID, err := db.CreateLeaveRecord(ctx, charlieID, "2024-01-17", "2024-01-17")
	require.NoError(t, err)
	err = engine.AssignCoversForLeave(ctx, leave2ID)
	require.NoError(t, err)

	// And a third leave for Jan 18 (Dave's day). Same — the future
	// cover must not pin successive new leaves to the same person.
	leave3ID, err := db.CreateLeaveRecord(ctx, daveID, "2024-01-18", "2024-01-18")
	require.NoError(t, err)
	err = engine.AssignCoversForLeave(ctx, leave3ID)
	require.NoError(t, err)

	cover1 := getCoverMemberID(t, ctx, db, "2024-01-16")
	cover2 := getCoverMemberID(t, ctx, db, "2024-01-17")
	cover3 := getCoverMemberID(t, ctx, db, "2024-01-18")

	members, err := db.GetActiveTeamMembers(ctx)
	require.NoError(t, err)
	names := make(map[string]string)
	for _, m := range members {
		names[m.ID] = m.Name
	}

	t.Logf("Cover for Jan 16 (Bob out):   %s", names[cover1])
	t.Logf("Cover for Jan 17 (Charlie):  %s", names[cover2])
	t.Logf("Cover for Jan 18 (Dave):     %s", names[cover3])

	// Regression assertion: successive new leaves must get different
	// covers regardless of any future cover planted in the table.
	// Under the old DB-anchored rotation, the future cover on Jan 26
	// would re-pin the rotation to Alice and findCover would start at
	// Bob for every new leave, making cover2 == cover3 == Bob.
	require.NotEqual(t, cover2, cover3, "The same person must not cover twice when other members are available")
}

// getCoverMemberID returns the member_id of the cover assignment for the
// given date, or "" if no cover is present.
func getCoverMemberID(t *testing.T, ctx context.Context, db *database.DB, date string) string {
	t.Helper()
	assignments, err := db.GetAssignmentsByDate(ctx, date)
	require.NoError(t, err)
	for _, a := range assignments {
		if a.IsCover {
			return a.MemberID
		}
	}
	return ""
}

// memberIDByName returns the ID of the team member whose name matches.
// Test fixture helper: each test sets up a small team with unique
// names, so a name lookup is unambiguous. Fails the test if no
// member matches.
func memberIDByName(t *testing.T, db *database.DB, name string) string {
	t.Helper()
	members, err := db.GetActiveTeamMembers(context.Background())
	require.NoError(t, err)
	for _, m := range members {
		if m.Name == name {
			return m.ID
		}
	}
	t.Fatalf("member %s not found", name)
	return ""
}
