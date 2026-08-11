package api

import (
	"testing"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSelectSupportMember pins the support-member picker used by the
// presence handler. It is a pure function with two precedence rules:
//
//  1. A cover assignment always wins over an original.
//  2. Within the same kind, the first assignment in the slice wins.
//
// The second return value distinguishes the chosen row's kind so the
// caller can render the correct badge ("cover" vs "HAT").
//
// A regression here misroutes the /api/v1/presence/today response:
// the wrong member would be flagged as today's support, or the
// cover badge would disappear, or the response would show nil with
// no obvious cause.
func TestSelectSupportMember(t *testing.T) {
	t.Parallel()

	// Two-person team — keep the test inputs readable.
	alice := database.TeamMember{ID: "alice", Name: "Alice", Email: "alice@example.com"}
	bob := database.TeamMember{ID: "bob", Name: "Bob", Email: "bob@example.com"}
	memberMap := map[string]database.TeamMember{
		"alice": alice,
		"bob":   bob,
	}

	t.Run("EmptyAssignments", func(t *testing.T) {
		t.Parallel()

		got, isCover := selectSupportMember(nil, memberMap)
		assert.Nil(t, got)
		assert.False(t, isCover)

		got, isCover = selectSupportMember([]database.RotaAssignment{}, memberMap)
		assert.Nil(t, got)
		assert.False(t, isCover)
	})

	t.Run("OnlyOriginal", func(t *testing.T) {
		t.Parallel()

		assignments := []database.RotaAssignment{
			{MemberID: "alice", IsCover: false},
		}
		got, isCover := selectSupportMember(assignments, memberMap)
		require.NotNil(t, got)
		assert.Equal(t, "alice", got.ID)
		assert.False(t, isCover, "an original row must surface as isCover=false")
	})

	t.Run("OnlyCover", func(t *testing.T) {
		t.Parallel()

		assignments := []database.RotaAssignment{
			{MemberID: "bob", IsCover: true},
		}
		got, isCover := selectSupportMember(assignments, memberMap)
		require.NotNil(t, got)
		assert.Equal(t, "bob", got.ID)
		assert.True(t, isCover, "a cover row must surface as isCover=true")
	})

	t.Run("CoverWinsOverOriginalRegardlessOfOrder", func(t *testing.T) {
		t.Parallel()

		// Precedence is by IsCover flag, not by slice order. Cover is
		// first → cover wins. Original is first → cover still wins.
		// Both orderings must produce the same result so a future
		// refactor that switches to "first row wins" doesn't break the
		// intent.
		t.Run("CoverFirst", func(t *testing.T) {
			t.Parallel()
			assignments := []database.RotaAssignment{
				{MemberID: "bob", IsCover: true},
				{MemberID: "alice", IsCover: false},
			}
			got, isCover := selectSupportMember(assignments, memberMap)
			require.NotNil(t, got)
			assert.Equal(t, "bob", got.ID)
			assert.True(t, isCover)
		})

		t.Run("OriginalFirst", func(t *testing.T) {
			t.Parallel()
			assignments := []database.RotaAssignment{
				{MemberID: "alice", IsCover: false},
				{MemberID: "bob", IsCover: true},
			}
			got, isCover := selectSupportMember(assignments, memberMap)
			require.NotNil(t, got)
			assert.Equal(t, "bob", got.ID)
			assert.True(t, isCover, "a cover row beats an original regardless of slice order")
		})
	})

	t.Run("CoverMissingFromMemberMapFallsBackToOriginal", func(t *testing.T) {
		t.Parallel()

		// The cover's MemberID isn't in the map (e.g., a team member
		// was deactivated since the assignment was created). The
		// picker must skip the orphan and fall back to the original
		// rather than returning nil and losing the day entirely.
		assignments := []database.RotaAssignment{
			{MemberID: "ghost", IsCover: true},
			{MemberID: "alice", IsCover: false},
		}
		got, isCover := selectSupportMember(assignments, memberMap)
		require.NotNil(t, got, "an orphan cover must not collapse the response to nil")
		assert.Equal(t, "alice", got.ID, "the picker must fall back to the original")
		assert.False(t, isCover, "the fallback row surfaces as isCover=false")
	})

	t.Run("AllMembersMissingFromMap", func(t *testing.T) {
		t.Parallel()

		// Every MemberID points to a member the map doesn't know
		// about — neither cover nor original can be resolved.
		assignments := []database.RotaAssignment{
			{MemberID: "ghost-1", IsCover: true},
			{MemberID: "ghost-2", IsCover: false},
		}
		got, isCover := selectSupportMember(assignments, memberMap)
		assert.Nil(t, got, "no resolvable member means a nil result")
		assert.False(t, isCover)
	})

	t.Run("MultipleCoversFirstWins", func(t *testing.T) {
		t.Parallel()

		// Two cover rows in the input. The picker must return the
		// first one it encounters, not the last. Pin the iteration
		// direction so a future "sort by date then take first"
		// refactor is caught.
		assignments := []database.RotaAssignment{
			{MemberID: "bob", IsCover: true},
			{MemberID: "alice", IsCover: true},
		}
		got, isCover := selectSupportMember(assignments, memberMap)
		require.NotNil(t, got)
		assert.Equal(t, "bob", got.ID, "first cover in iteration order wins")
		assert.True(t, isCover)
	})

	t.Run("MultipleOriginalsFirstWins", func(t *testing.T) {
		t.Parallel()

		// Mirror of the multi-cover case for the fallback path:
		// when no cover is present, the first original in iteration
		// order wins.
		assignments := []database.RotaAssignment{
			{MemberID: "alice", IsCover: false},
			{MemberID: "bob", IsCover: false},
		}
		got, isCover := selectSupportMember(assignments, memberMap)
		require.NotNil(t, got)
		assert.Equal(t, "alice", got.ID, "first original in iteration order wins")
		assert.False(t, isCover)
	})
}
