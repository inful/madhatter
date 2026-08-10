package web

import (
	"testing"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildScheduleMatrix pins the dashboard's presence-to-matrix mapping:
//   - one row per unique member across all days, sorted by name
//   - one cell per (member, day) with status derived from Present/WFH/Away
//   - AtWFHFloor true when atWork <= floor (and floor > 0)
//   - Assigned/Swapped/SwapInfo propagated when the day's Assigned matches
func TestBuildScheduleMatrix(t *testing.T) {
	t.Parallel()

	alice := database.TeamMember{ID: "alice", Name: "Alice"}
	bob := database.TeamMember{ID: "bob", Name: "Bob"}
	carol := database.TeamMember{ID: "carol", Name: "Carol"}

	t.Run("empty input yields empty matrix", func(t *testing.T) {
		t.Parallel()

		got := buildScheduleMatrix(nil, 0)
		assert.Empty(t, got.Days)
		assert.Empty(t, got.Rows)
	})

	t.Run("single day single onsite member", func(t *testing.T) {
		t.Parallel()

		got := buildScheduleMatrix([]presenceDay{
			{DateISO: "2026-05-20", DateDisplay: "Wed 20 May", Present: []database.TeamMember{alice}},
		}, 0)

		require.Len(t, got.Days, 1)
		assert.Equal(t, 1, got.Days[0].AtWorkCount)
		assert.Equal(t, 0, got.Days[0].WFHCount)
		assert.Equal(t, 0, got.Days[0].LeaveCount)
		assert.False(t, got.Days[0].AtWFHFloor, "floor=0 must never set AtWFHFloor")

		require.Len(t, got.Rows, 1)
		assert.Equal(t, "alice", got.Rows[0].Member.ID)
		require.Len(t, got.Rows[0].Cells, 1)
		assert.Equal(t, "onsite", got.Rows[0].Cells[0].Status)
		assert.Equal(t, "On-site", got.Rows[0].Cells[0].Label)
		assert.False(t, got.Rows[0].Cells[0].Assigned)
	})

	t.Run("rows sorted by name across multiple members", func(t *testing.T) {
		t.Parallel()

		got := buildScheduleMatrix([]presenceDay{
			{
				DateISO: "2026-05-20", DateDisplay: "Wed 20 May",
				Present: []database.TeamMember{alice},
				WFH:     []database.TeamMember{bob},
				Away:    []presenceLeave{{Member: carol}},
			},
		}, 0)

		require.Len(t, got.Rows, 3)
		assert.Equal(t, "Alice", got.Rows[0].Member.Name)
		assert.Equal(t, "Bob", got.Rows[1].Member.Name)
		assert.Equal(t, "Carol", got.Rows[2].Member.Name)
	})

	t.Run("member deduplicated across days", func(t *testing.T) {
		t.Parallel()

		got := buildScheduleMatrix([]presenceDay{
			{DateISO: "2026-05-20", DateDisplay: "Wed 20 May", WFH: []database.TeamMember{alice}},
			{DateISO: "2026-05-21", DateDisplay: "Thu 21 May", Present: []database.TeamMember{alice}},
		}, 0)

		require.Len(t, got.Rows, 1, "Alice appears once even though she is named in two days")
		require.Len(t, got.Rows[0].Cells, 2)
		assert.Equal(t, "wfh", got.Rows[0].Cells[0].Status)
		assert.Equal(t, "WFH", got.Rows[0].Cells[0].Label)
		assert.Equal(t, "onsite", got.Rows[0].Cells[1].Status)
	})

	t.Run("AtWFHFloor set when atWork equals floor", func(t *testing.T) {
		t.Parallel()

		got := buildScheduleMatrix([]presenceDay{
			{DateISO: "2026-05-20", DateDisplay: "Wed 20 May", Present: []database.TeamMember{alice}},
		}, 1)
		assert.True(t, got.Days[0].AtWFHFloor, "atWork=1 <= floor=1 must set AtWFHFloor")
	})

	t.Run("AtWFHFloor set when atWork under floor", func(t *testing.T) {
		t.Parallel()

		got := buildScheduleMatrix([]presenceDay{
			{DateISO: "2026-05-20", DateDisplay: "Wed 20 May", Present: []database.TeamMember{alice}},
		}, 5)
		assert.True(t, got.Days[0].AtWFHFloor)
	})

	t.Run("AtWFHFloor unset when atWork above floor", func(t *testing.T) {
		t.Parallel()

		got := buildScheduleMatrix([]presenceDay{
			{DateISO: "2026-05-20", DateDisplay: "Wed 20 May", Present: []database.TeamMember{alice, bob, carol}},
		}, 1)
		assert.False(t, got.Days[0].AtWFHFloor, "atWork=3 > floor=1 must not set AtWFHFloor")
	})

	t.Run("assigned swap info propagated to cell", func(t *testing.T) {
		t.Parallel()

		got := buildScheduleMatrix([]presenceDay{
			{
				DateISO: "2026-05-20", DateDisplay: "Wed 20 May",
				Assigned:         &database.TeamMember{ID: "alice", Name: "Alice"},
				AssignedSwapped:  true,
				AssignedSwapInfo: "Alice <-> Bob",
				Present:          []database.TeamMember{alice},
			},
		}, 0)

		assert.True(t, got.Rows[0].Cells[0].Assigned)
		assert.True(t, got.Rows[0].Cells[0].Swapped)
		assert.Equal(t, "Alice <-> Bob", got.Rows[0].Cells[0].SwapInfo)
	})

	t.Run("IsToday propagated to both day and cell", func(t *testing.T) {
		t.Parallel()

		got := buildScheduleMatrix([]presenceDay{
			{DateISO: "2026-05-20", DateDisplay: "Wed 20 May", IsToday: true, Present: []database.TeamMember{alice}},
		}, 0)

		assert.True(t, got.Days[0].IsToday)
		assert.True(t, got.Rows[0].Cells[0].IsToday)
		assert.Equal(t, "2026-05-20", got.Rows[0].Cells[0].DateISO)
		assert.Equal(t, "Wed 20 May", got.Rows[0].Cells[0].DateLabel)
	})

	t.Run("away status takes precedence over wfh", func(t *testing.T) {
		t.Parallel()

		got := buildScheduleMatrix([]presenceDay{
			{
				DateISO: "2026-05-20", DateDisplay: "Wed 20 May",
				WFH:  []database.TeamMember{alice},
				Away: []presenceLeave{{Member: alice}},
			},
		}, 0)

		assert.Equal(t, "away", got.Rows[0].Cells[0].Status, "a member both WFH and Away must surface as Away")
		assert.Equal(t, "Away", got.Rows[0].Cells[0].Label)
	})
}
