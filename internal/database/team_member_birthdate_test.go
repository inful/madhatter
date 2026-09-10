package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAddTeamMember_WithBirthdate pins the happy path for issue #60:
// an admin can record a member's birthdate at add time. The value
// survives GetMemberByID / GetActiveTeamMembers and the resulting
// *time.Time carries the same Y-M-D as the input string.
func TestAddTeamMember_WithBirthdate(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupTestDB(t)
	defer cleanup()

	bday := time.Date(1990, 5, 15, 0, 0, 0, 0, time.UTC)
	id, err := db.AddTeamMember(ctx, "Alice", "alice@example.com", &bday)
	require.NoError(t, err)

	got, err := db.GetMemberByID(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, got.Birthdate, "birthdate must survive the round-trip")
	assert.Equal(t, "1990-05-15", got.Birthdate.Format("2006-01-02"))
}

// TestAddTeamMember_WithoutBirthdate confirms a nil pointer
// is accepted and stored as SQL NULL. The Go wrapper returns
// nil (not a zero time) so callers can distinguish "not set"
// from "set to today's date" — a critical distinction for
// the dashboard probe.
func TestAddTeamMember_WithoutBirthdate(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupTestDB(t)
	defer cleanup()

	id, err := db.AddTeamMember(ctx, "Bob", "bob@example.com", nil)
	require.NoError(t, err)

	got, err := db.GetMemberByID(ctx, id)
	require.NoError(t, err)
	assert.Nil(t, got.Birthdate,
		"a nil input must round-trip as nil (SQL NULL), not as a zero time")
}

// TestUpdateTeamMember_Birthdate pins the edit path. The wrapper
// must accept a non-nil pointer to overwrite the existing value.
func TestUpdateTeamMember_Birthdate(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupTestDB(t)
	defer cleanup()

	original := time.Date(1990, 5, 15, 0, 0, 0, 0, time.UTC)
	id, err := db.AddTeamMember(ctx, "Alice", "alice@example.com", &original)
	require.NoError(t, err)

	updated := time.Date(1992, 9, 3, 0, 0, 0, 0, time.UTC)
	require.NoError(t, db.UpdateTeamMember(ctx, id, "Alice Cooper", "alice@example.com", &updated))

	got, err := db.GetMemberByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "Alice Cooper", got.Name)
	require.NotNil(t, got.Birthdate)
	assert.Equal(t, "1992-09-03", got.Birthdate.Format("2006-01-02"))
}

// TestUpdateTeamMember_ClearBirthdate pins the "admin removed
// the birthdate" path. Passing nil must clear the column back
// to SQL NULL, not persist a zero time that would falsely
// match everyone born on 0001-01-01.
func TestUpdateTeamMember_ClearBirthdate(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupTestDB(t)
	defer cleanup()

	original := time.Date(1990, 5, 15, 0, 0, 0, 0, time.UTC)
	id, err := db.AddTeamMember(ctx, "Alice", "alice@example.com", &original)
	require.NoError(t, err)

	require.NoError(t, db.UpdateTeamMember(ctx, id, "Alice", "alice@example.com", nil))

	got, err := db.GetMemberByID(ctx, id)
	require.NoError(t, err)
	assert.Nil(t, got.Birthdate,
		"passing nil to UpdateTeamMember must clear the column to SQL NULL")
}

// TestGetUpcomingBirthdays_TodayOnly pins the happy path for
// the dashboard banner. A member with a birthdate today
// (MM-DD match, year ignored) must appear with DaysUntil=0.
func TestGetUpcomingBirthdays_TodayOnly(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupTestDB(t)
	defer cleanup()

	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	bday := time.Date(1990, 6, 15, 0, 0, 0, 0, time.UTC)
	id, err := db.AddTeamMember(ctx, "Alice", "alice@example.com", &bday)
	require.NoError(t, err)

	got, err := db.GetUpcomingBirthdays(ctx, now, 7)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, id, got[0].MemberID)
	assert.Equal(t, "Alice", got[0].Name)
	assert.Equal(t, 0, got[0].DaysUntil, "today's birthday is DaysUntil=0")
	assert.Equal(t, "06-15", got[0].BirthdayMonthDay)
	// BirthYear is preserved for admin auditing — the dashboard
	// copy must NOT surface it. See DashboardHideYear assertion.
	assert.Equal(t, 1990, got[0].BirthYear)
}

// TestGetUpcomingBirthdays_ExcludesNull pins that members
// without a birthdate never appear in the banner — there's no
// row to celebrate. This is the safety net for the dashboard
// probe joining on birthdate IS NOT NULL.
func TestGetUpcomingBirthdays_ExcludesNull(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupTestDB(t)
	defer cleanup()

	now := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	_, err := db.AddTeamMember(ctx, "Bob", "bob@example.com", nil)
	require.NoError(t, err)

	got, err := db.GetUpcomingBirthdays(ctx, now, 7)
	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestGetUpcomingBirthdays_WindowBoundary pins the "7 days
// from today" cutoff: a birthday at exactly today+7 is in
// scope, today+8 is out.
func TestGetUpcomingBirthdays_WindowBoundary(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupTestDB(t)
	defer cleanup()

	now := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

	inScope := time.Date(1990, 6, 22, 0, 0, 0, 0, time.UTC) // today+7
	outOfScope := time.Date(1990, 6, 23, 0, 0, 0, 0, time.UTC)

	_, err := db.AddTeamMember(ctx, "In", "in@example.com", &inScope)
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Out", "out@example.com", &outOfScope)
	require.NoError(t, err)

	got, err := db.GetUpcomingBirthdays(ctx, now, 7)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "In", got[0].Name)
	assert.Equal(t, 7, got[0].DaysUntil)
}

// TestGetUpcomingBirthdays_MonthWrap pins the year-boundary
// case. Today is Dec 28; a member born on Jan 3 is "in 6 days"
// even though the calendar year changes. The SQL has to use
// MM-DD arithmetic, not literal date comparison.
func TestGetUpcomingBirthdays_MonthWrap(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupTestDB(t)
	defer cleanup()

	now := time.Date(2026, 12, 28, 0, 0, 0, 0, time.UTC)
	bday := time.Date(1990, 1, 3, 0, 0, 0, 0, time.UTC) // 6 days from Dec 28
	_, err := db.AddTeamMember(ctx, "NewYear", "ny@example.com", &bday)
	require.NoError(t, err)

	got, err := db.GetUpcomingBirthdays(ctx, now, 7)
	require.NoError(t, err)
	require.Len(t, got, 1, "Dec 28 + 7 days must wrap to early January")
	assert.Equal(t, "NewYear", got[0].Name)
	assert.Equal(t, 6, got[0].DaysUntil)
}

// TestGetUpcomingBirthdays_DateAffinity confirms the query
// uses the same julianday()-based comparison the WFH / leave
// modules adopted for date-affinity correctness (so a literal
// "YYYY-MM-DD" stored as TEXT compares correctly with a
// time.Time parameter).
func TestGetUpcomingBirthdays_DateAffinity(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupTestDB(t)
	defer cleanup()

	now := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	bday := time.Date(1990, 6, 18, 0, 0, 0, 0, time.UTC)
	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com", &bday)
	require.NoError(t, err)

	got, err := db.GetUpcomingBirthdays(ctx, now, 7)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, 3, got[0].DaysUntil)
}
