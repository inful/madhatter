package calendar

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newCalendarTestDB(t *testing.T) *database.DB {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	t.Setenv("MIGRATIONS_PATH", filepath.Join(repoRoot, "migrations"))
	db, err := database.New(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// stubMaterialiser records the date ranges it was asked to ensure.
type stubMaterialiser struct {
	calls []stubMaterialiserCall
}

type stubMaterialiserCall struct {
	start, end time.Time
}

func (s *stubMaterialiser) EnsureRecurringMaterialized(_ context.Context, start, end time.Time) (int, error) {
	s.calls = append(s.calls, stubMaterialiserCall{start: start, end: end})
	return 0, nil
}

// stubHolidayLookup records whether a given date is a holiday.
type stubHolidayLookup struct {
	holidays map[string]string
}

func (s *stubHolidayLookup) GetHoliday(dateStr string) (string, bool) {
	if s.holidays == nil {
		return "", false
	}
	name, ok := s.holidays[dateStr]
	return name, ok
}

func TestComputePresenceSnapshot_EmptyDB(t *testing.T) {
	ctx := context.Background()
	db := newCalendarTestDB(t)

	b := newPresenceBuilder(db, nil, nil, "seed")
	snap, err := b.Build(ctx, "2026-06-10")
	require.NoError(t, err)

	assert.Equal(t, "2026-06-10", snap.Date)
	assert.False(t, snap.IsWeekend)
	assert.False(t, snap.IsHoliday)
	assert.Equal(t, 0, snap.TotalActive)
	assert.Empty(t, snap.OnSite)
	assert.Empty(t, snap.OnLeave)
	assert.Empty(t, snap.WFH)
	assert.Empty(t, snap.HATName)
}

func TestComputePresenceSnapshot_TwoOnLeaveThreeOnSite(t *testing.T) {
	ctx := context.Background()
	db := newCalendarTestDB(t)

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Carol", "carol@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Dave", "dave@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Eve", "eve@example.com")
	require.NoError(t, err)

	// Alice and Bob on leave today.
	_, err = db.CreateLeaveRecord(ctx, aliceID, "2026-06-10", "2026-06-10")
	require.NoError(t, err)
	_, err = db.CreateLeaveRecord(ctx, bobID, "2026-06-10", "2026-06-10")
	require.NoError(t, err)

	b := newPresenceBuilder(db, nil, nil, "seed")
	snap, err := b.Build(ctx, "2026-06-10")
	require.NoError(t, err)

	assert.Equal(t, 5, snap.TotalActive)
	assert.Len(t, snap.OnLeave, 2)
	assert.Empty(t, snap.WFH)
	assert.Len(t, snap.OnSite, 3)
}

func TestComputePresenceSnapshot_WFHIncluded(t *testing.T) {
	ctx := context.Background()
	db := newCalendarTestDB(t)

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	err = db.CreateApprovedRecurringWFHRequest(ctx, aliceID, "2026-06-10", time.Now().UTC())
	require.NoError(t, err)

	b := newPresenceBuilder(db, nil, nil, "seed")
	snap, err := b.Build(ctx, "2026-06-10")
	require.NoError(t, err)

	assert.Equal(t, 1, snap.TotalActive)
	assert.Len(t, snap.WFH, 1)
	assert.Equal(t, "Alice", snap.WFH[0].Name)
	assert.Empty(t, snap.OnSite)
}

func TestComputePresenceSnapshot_HATNonCover(t *testing.T) {
	ctx := context.Background()
	db := newCalendarTestDB(t)

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	_, err = db.CreateRotaAssignment(ctx, "2026-06-10", aliceID, false, nil)
	require.NoError(t, err)

	b := newPresenceBuilder(db, nil, nil, "seed")
	snap, err := b.Build(ctx, "2026-06-10")
	require.NoError(t, err)
	assert.Equal(t, "Alice", snap.HATName)
	assert.False(t, snap.HATIsCover)
	assert.Equal(t, aliceID, snap.HATMemberID)
}

func TestComputePresenceSnapshot_HATCoverOnly(t *testing.T) {
	ctx := context.Background()
	db := newCalendarTestDB(t)

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	_, err = db.CreateRotaAssignment(ctx, "2026-06-10", aliceID, true, nil)
	require.NoError(t, err)

	b := newPresenceBuilder(db, nil, nil, "seed")
	snap, err := b.Build(ctx, "2026-06-10")
	require.NoError(t, err)
	assert.Equal(t, "Alice", snap.HATName)
	assert.True(t, snap.HATIsCover)
}

func TestComputePresenceSnapshot_StableOrderSameSeed(t *testing.T) {
	ctx := context.Background()
	db := newCalendarTestDB(t)
	for _, name := range []string{"Alice", "Bob", "Carol", "Dave"} {
		_, err := db.AddTeamMember(ctx, name, name+"@example.com")
		require.NoError(t, err)
	}

	b1 := newPresenceBuilder(db, nil, nil, "salt")
	s1, err := b1.Build(ctx, "2026-06-10")
	require.NoError(t, err)
	b2 := newPresenceBuilder(db, nil, nil, "salt")
	s2, err := b2.Build(ctx, "2026-06-10")
	require.NoError(t, err)

	require.Len(t, s1.ShuffledOrder, 4)
	require.Len(t, s2.ShuffledOrder, 4)
	for i := range s1.ShuffledOrder {
		assert.Equal(t, s1.ShuffledOrder[i].Name, s2.ShuffledOrder[i].Name, "position %d", i)
	}
}

func TestComputePresenceSnapshot_DifferentSeedDifferentOrder(t *testing.T) {
	ctx := context.Background()
	db := newCalendarTestDB(t)
	for _, name := range []string{"Alice", "Bob", "Carol", "Dave", "Eve", "Frank"} {
		_, err := db.AddTeamMember(ctx, name, name+"@example.com")
		require.NoError(t, err)
	}

	b1 := newPresenceBuilder(db, nil, nil, "salt-1")
	s1, err := b1.Build(ctx, "2026-06-10")
	require.NoError(t, err)
	b2 := newPresenceBuilder(db, nil, nil, "salt-2")
	s2, err := b2.Build(ctx, "2026-06-10")
	require.NoError(t, err)

	// With six members and two distinct salts, the chance of identical
	// orderings is 1/720, so a mismatch is essentially guaranteed.
	require.Len(t, s1.ShuffledOrder, 6)
	require.Len(t, s2.ShuffledOrder, 6)
	matches := 0
	for i := range s1.ShuffledOrder {
		if s1.ShuffledOrder[i].Name == s2.ShuffledOrder[i].Name {
			matches++
		}
	}
	assert.Less(t, matches, 6, "expected different seeds to produce different orderings")
}

func TestComputePresenceSnapshot_MaterialiserCalled(t *testing.T) {
	ctx := context.Background()
	db := newCalendarTestDB(t)
	mat := &stubMaterialiser{}

	b := newPresenceBuilder(db, mat, nil, "seed")
	date := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	_, err := b.Build(ctx, date.Format("2006-01-02"))
	require.NoError(t, err)

	require.Len(t, mat.calls, 1)
	assert.True(t, mat.calls[0].start.Equal(date))
	assert.True(t, mat.calls[0].end.Equal(date))
}

func TestComputePresenceSnapshot_NilMaterialiserOK(t *testing.T) {
	ctx := context.Background()
	db := newCalendarTestDB(t)
	b := newPresenceBuilder(db, nil, nil, "seed")
	_, err := b.Build(ctx, "2026-06-10")
	require.NoError(t, err)
}

func TestComputePresenceSnapshot_HolidayLookup(t *testing.T) {
	ctx := context.Background()
	db := newCalendarTestDB(t)
	hl := &stubHolidayLookup{holidays: map[string]string{"2026-06-10": "Test Holiday"}}

	b := newPresenceBuilder(db, nil, hl, "seed")
	snap, err := b.Build(ctx, "2026-06-10")
	require.NoError(t, err)
	assert.True(t, snap.IsHoliday)
	assert.Equal(t, "Test Holiday", snap.HolidayName)
}

func TestComputePresenceSnapshot_WeekendFlag(t *testing.T) {
	ctx := context.Background()
	db := newCalendarTestDB(t)
	b := newPresenceBuilder(db, nil, nil, "seed")
	// 2026-06-13 is a Saturday.
	snap, err := b.Build(ctx, "2026-06-13")
	require.NoError(t, err)
	assert.True(t, snap.IsWeekend)
}
