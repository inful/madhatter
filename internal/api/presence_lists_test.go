package api

import (
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildPresenceListsWithWFH pins the api presence grouping: members
// are categorized into present / away / wfh based on leave records,
// explicit WFH requests, and the member's contractual recurring-WFH
// weekday. The function is the single source of truth for the
// /api/v1/presence/today response shape.
func TestBuildPresenceListsWithWFH(t *testing.T) {
	t.Parallel()

	// Wednesday — picked so the recurring-WFH tests can flip a single
	// bool and observe the outcome deterministically.
	date := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)

	alice := database.TeamMember{ID: "alice", Name: "Alice"}
	bob := database.TeamMember{ID: "bob", Name: "Bob"}
	carol := database.TeamMember{ID: "carol", Name: "Carol"}
	dave := database.TeamMember{ID: "dave", Name: "Dave"}
	// Eric has a contractual recurring WFH on Wednesday.
	eric := database.TeamMember{ID: "eric", Name: "Eric", RecurringWFHWednesday: true}
	// Frank has a contractual recurring WFH on Wednesday AND is on leave — away must win.
	frank := database.TeamMember{ID: "frank", Name: "Frank", RecurringWFHWednesday: true}
	// Grace has a contractual recurring WFH on Wednesday AND has an explicit WFH request — must not double-count.
	grace := database.TeamMember{ID: "grace", Name: "Grace", RecurringWFHWednesday: true}

	t.Run("empty inputs yield empty outputs", func(t *testing.T) {
		t.Parallel()

		present, away, wfhList := buildPresenceListsWithWFH(nil, nil, nil, date)
		assert.Empty(t, present)
		assert.Empty(t, away)
		assert.Empty(t, wfhList)
	})

	t.Run("memberMap only present", func(t *testing.T) {
		t.Parallel()

		memberMap := map[string]database.TeamMember{"alice": alice, "bob": bob}
		present, away, wfhList := buildPresenceListsWithWFH(memberMap, nil, nil, date)

		require.Len(t, present, 2)
		assert.Equal(t, "Alice", present[0].Name)
		assert.Equal(t, "Bob", present[1].Name, "present list must be sorted by name")
		assert.Empty(t, away)
		assert.Empty(t, wfhList)
	})

	t.Run("leave records surface as away", func(t *testing.T) {
		t.Parallel()

		memberMap := map[string]database.TeamMember{"alice": alice}
		leave := []database.LeaveRecord{{MemberID: "alice"}}

		present, away, wfhList := buildPresenceListsWithWFH(memberMap, leave, nil, date)

		assert.Empty(t, present)
		require.Len(t, away, 1)
		assert.Equal(t, "alice", away[0].ID)
		assert.Empty(t, wfhList)
	})

	t.Run("explicit WFH request surfaces as wfh", func(t *testing.T) {
		t.Parallel()

		memberMap := map[string]database.TeamMember{"bob": bob}
		wfhRequests := []database.WFHRequest{{MemberID: "bob", Date: "2026-05-20"}}

		present, away, wfhList := buildPresenceListsWithWFH(memberMap, nil, wfhRequests, date)

		assert.Empty(t, present)
		assert.Empty(t, away)
		require.Len(t, wfhList, 1)
		assert.Equal(t, "bob", wfhList[0].ID)
	})

	t.Run("recurring WFH on the date surfaces as wfh", func(t *testing.T) {
		t.Parallel()

		memberMap := map[string]database.TeamMember{"eric": eric}
		present, away, wfhList := buildPresenceListsWithWFH(memberMap, nil, nil, date)

		assert.Empty(t, present)
		assert.Empty(t, away)
		require.Len(t, wfhList, 1)
		assert.Equal(t, "eric", wfhList[0].ID)
	})

	t.Run("recurring WFH on a different weekday does not surface as wfh", func(t *testing.T) {
		t.Parallel()

		// Eric's recurring WFH is Wednesday; querying for Tuesday must surface him as present.
		tuesday := time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC)
		memberMap := map[string]database.TeamMember{"eric": eric}

		present, away, wfhList := buildPresenceListsWithWFH(memberMap, nil, nil, tuesday)

		require.Len(t, present, 1)
		assert.Equal(t, "eric", present[0].ID)
		assert.Empty(t, away)
		assert.Empty(t, wfhList)
	})

	t.Run("leave beats recurring WFH", func(t *testing.T) {
		t.Parallel()

		memberMap := map[string]database.TeamMember{"frank": frank}
		leave := []database.LeaveRecord{{MemberID: "frank"}}

		present, away, wfhList := buildPresenceListsWithWFH(memberMap, leave, nil, date)

		assert.Empty(t, present)
		require.Len(t, away, 1)
		assert.Equal(t, "frank", away[0].ID)
		assert.Empty(t, wfhList, "leave must take precedence over recurring WFH")
	})

	t.Run("explicit WFH request dedups against recurring WFH", func(t *testing.T) {
		t.Parallel()

		memberMap := map[string]database.TeamMember{"grace": grace}
		wfhRequests := []database.WFHRequest{{MemberID: "grace", Date: "2026-05-20"}}

		_, _, wfhList := buildPresenceListsWithWFH(memberMap, nil, wfhRequests, date)

		require.Len(t, wfhList, 1, "recurring WFH must not double-count when an explicit WFH row already exists")
		assert.Equal(t, "grace", wfhList[0].ID)
	})

	t.Run("orphan leave record is ignored", func(t *testing.T) {
		t.Parallel()

		memberMap := map[string]database.TeamMember{"alice": alice}
		leave := []database.LeaveRecord{{MemberID: "ghost"}} // not in memberMap

		present, away, wfhList := buildPresenceListsWithWFH(memberMap, leave, nil, date)

		require.Len(t, present, 1)
		assert.Equal(t, "alice", present[0].ID)
		assert.Empty(t, away)
		assert.Empty(t, wfhList)
	})

	t.Run("orphan WFH request is ignored", func(t *testing.T) {
		t.Parallel()

		memberMap := map[string]database.TeamMember{"alice": alice}
		wfhRequests := []database.WFHRequest{{MemberID: "ghost"}}

		present, away, wfhList := buildPresenceListsWithWFH(memberMap, nil, wfhRequests, date)

		require.Len(t, present, 1)
		assert.Equal(t, "alice", present[0].ID)
		assert.Empty(t, away)
		assert.Empty(t, wfhList)
	})

	t.Run("mixed scenario sorted across all three lists", func(t *testing.T) {
		t.Parallel()

		memberMap := map[string]database.TeamMember{
			"alice": alice, "bob": bob, "carol": carol, "dave": dave, "eric": eric,
		}
		leave := []database.LeaveRecord{
			{MemberID: "carol"}, {MemberID: "dave"},
		}
		wfhRequests := []database.WFHRequest{{MemberID: "bob"}}

		present, away, wfhList := buildPresenceListsWithWFH(memberMap, leave, wfhRequests, date)

		require.Len(t, present, 1)
		assert.Equal(t, "Alice", present[0].Name)

		require.Len(t, away, 2)
		assert.Equal(t, "Carol", away[0].Name)
		assert.Equal(t, "Dave", away[1].Name)

		require.Len(t, wfhList, 2)
		assert.Equal(t, "Bob", wfhList[0].Name, "explicit WFH request surfaces before recurring WFH alphabetically")
		assert.Equal(t, "Eric", wfhList[1].Name)
	})
}
