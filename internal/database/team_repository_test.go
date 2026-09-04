package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTeamRepository_AsInterface checks that *DB satisfies the
// TeamRepository interface and that a caller can drive a team
// member through the contract. This is the characterisation test
// for the per-aggregate repo split (recommendation #1): if *DB
// ever loses a method signature, this test stops compiling.
func TestTeamRepository_AsInterface(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var repo TeamRepository = db

	ctx := context.Background()
	id, err := repo.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	require.NotEmpty(t, id)

	member, err := repo.GetMemberByID(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, member)
	assert.Equal(t, "Alice", member.Name)
	assert.Equal(t, "alice@example.com", member.Email)
	assert.True(t, member.IsActive)
}

// TestTeamRepository_RecurringWFHDays walks through the
// member-recurring-days accessor set. Bundled with the others so
// any signature drift on those methods also surfaces here.
func TestTeamRepository_RecurringWFHDays(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var repo TeamRepository = db
	ctx := context.Background()
	id, err := repo.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	require.NoError(t, repo.SetTeamMemberRecurringWFHDays(ctx, id, RecurringWFHDays{
		Monday:    true,
		Wednesday: true,
		Friday:    true,
	}))

	member, err := repo.GetMemberByID(ctx, id)
	require.NoError(t, err)
	assert.True(t, member.IsRecurringWFHOn(time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)))  // Monday
	assert.False(t, member.IsRecurringWFHOn(time.Date(2026, 1, 6, 0, 0, 0, 0, time.UTC))) // Tuesday
	assert.True(t, member.IsRecurringWFHOn(time.Date(2026, 1, 7, 0, 0, 0, 0, time.UTC)))  // Wednesday
	assert.True(t, member.IsRecurringWFHOn(time.Date(2026, 1, 9, 0, 0, 0, 0, time.UTC)))  // Friday
}

// TestTeamRepository_ExemptAndPermanent Pins the two admin
// toggles on team members. These flags drive the picker exclusion
// list, so any signature change should fail this test loud.
func TestTeamRepository_ExemptAndPermanent(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	var repo TeamRepository = db
	ctx := context.Background()
	id, err := repo.AddTeamMember(ctx, "Carol", "carol@example.com")
	require.NoError(t, err)

	require.NoError(t, repo.SetTeamMemberExemptFromAssignment(ctx, id, true))
	require.NoError(t, repo.SetTeamMemberPermanentWFH(ctx, id, true))

	member, err := repo.GetMemberByID(ctx, id)
	require.NoError(t, err)
	assert.True(t, member.IsExemptFromAssignment, "exempt flag should round-trip")
	assert.True(t, member.IsPermanentWFH, "permanent-WFH flag should round-trip")
	assert.True(t, member.HasPermanentRecurringWFH(),
		"all-weekdays-on should imply permanent-WFH")
}
