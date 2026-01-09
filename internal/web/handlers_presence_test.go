package web

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
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

	_, err = db.CreateLeaveRecord(ctx, bobID, "vacation", "2024-01-08", "2024-01-08")
	require.NoError(t, err)

	holidayChecker := func(date time.Time) bool {
		holiday := time.Date(2024, time.January, 9, 0, 0, 0, 0, time.UTC)
		return date.Equal(holiday)
	}

	handler := &Handler{db: db, holidayChecker: holidayChecker}
	start := time.Date(2024, time.January, 5, 0, 0, 0, 0, time.UTC)

	presence, err := handler.getUpcomingPresenceFrom(ctx, start)
	require.NoError(t, err)
	require.Len(t, presence, 5)

	require.Equal(t, "2024-01-05", presence[0].DateISO)
	require.Equal(t, "2024-01-08", presence[1].DateISO)
	require.Equal(t, "2024-01-10", presence[2].DateISO)
	require.Equal(t, "2024-01-11", presence[3].DateISO)
	require.Equal(t, "2024-01-12", presence[4].DateISO)

	require.Len(t, presence[0].Present, 2)
	require.Len(t, presence[1].Present, 1)
	require.Equal(t, "Alice", presence[1].Present[0].Name)
	require.Len(t, presence[1].Away, 1)
	require.Equal(t, "Bob", presence[1].Away[0].Member.Name)
}
