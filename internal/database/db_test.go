package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// setupTestDB creates a temporary test database.
func setupTestDB(t *testing.T) (*DB, func()) {
	t.Helper()

	// Create temporary database in temp directory
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := New(dbPath)
	require.NoError(t, err, "Failed to create test database")

	cleanup := func() {
		_ = db.Close()
	}

	return db, cleanup
}

func TestAddTeamMember_Success(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Act
	id, err := db.AddTeamMember(ctx, "Alice Johnson", "alice@example.com")

	// Assert
	require.NoError(t, err)
	require.NotEmpty(t, id)

	// Verify in database
	members, err := db.GetActiveTeamMembers(ctx)
	require.NoError(t, err)
	require.Len(t, members, 1)
	require.Equal(t, "Alice Johnson", members[0].Name)
	require.Equal(t, "alice@example.com", members[0].Email)
	require.True(t, members[0].IsActive)
}

func TestAddTeamMember_DuplicateEmail(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	// Act
	_, err = db.AddTeamMember(ctx, "Alice 2", "alice@example.com")

	// Assert
	require.Error(t, err)
	require.Contains(t, err.Error(), "UNIQUE")
}

func TestAddTeamMember_EmptyName(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Act
	_, err := db.AddTeamMember(ctx, "", "alice@example.com")

	// Assert
	require.Error(t, err)
}

func TestGetActiveTeamMembers_Empty(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Act
	members, err := db.GetActiveTeamMembers(ctx)

	// Assert
	require.NoError(t, err)
	require.Empty(t, members)
}

func TestGetActiveTeamMembers_OnlyActive(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add active members
	id1, _ := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	id2, _ := db.AddTeamMember(ctx, "Bob", "bob@example.com")

	// Deactivate one
	_, err := db.ExecContext(ctx, "UPDATE team_members SET is_active = 0 WHERE id = ?", id1)
	require.NoError(t, err)

	// Act
	members, err := db.GetActiveTeamMembers(ctx)

	// Assert
	require.NoError(t, err)
	require.Len(t, members, 1)
	require.Equal(t, "Bob", members[0].Name)
	require.Equal(t, id2, members[0].ID)
}

func TestGetMemberByEmail_Found(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	// Act
	member, err := db.GetMemberByEmail(ctx, "alice@example.com")

	// Assert
	require.NoError(t, err)
	require.Equal(t, "Alice", member.Name)
	require.Equal(t, "alice@example.com", member.Email)
}

func TestGetMemberByEmail_NotFound(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Act
	_, err := db.GetMemberByEmail(ctx, "nonexistent@example.com")

	// Assert
	require.Error(t, err)
	require.Equal(t, sql.ErrNoRows, err)
}

func TestGetLatestAssignmentDate_Empty(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Act
	latest, err := db.GetLatestAssignmentDate(ctx)

	// Assert
	require.NoError(t, err)
	require.Empty(t, latest)
}

func TestGetLatestAssignmentDate_WithAssignments(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add team members
	id1, _ := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	id2, _ := db.AddTeamMember(ctx, "Bob", "bob@example.com")

	// Add assignments
	today := "2025-01-15"
	tomorrow := "2025-01-16"
	_, err := db.CreateRotaAssignment(ctx, today, id1, false, nil)
	require.NoError(t, err)
	_, err = db.CreateRotaAssignment(ctx, tomorrow, id2, false, nil)
	require.NoError(t, err)

	// Act
	latest, err := db.GetLatestAssignmentDate(ctx)

	// Assert
	require.NoError(t, err)
	require.Equal(t, tomorrow, latest)
}

func TestGetAssignmentsByDateRange(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Add team members
	id1, _ := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	id2, _ := db.AddTeamMember(ctx, "Bob", "bob@example.com")

	// Add assignments
	startDate := "2025-01-15"
	midDate := "2025-01-16"
	endDate := "2025-01-17"
	_, err := db.CreateRotaAssignment(ctx, startDate, id1, false, nil)
	require.NoError(t, err)
	_, err = db.CreateRotaAssignment(ctx, midDate, id2, false, nil)
	require.NoError(t, err)
	_, err = db.CreateRotaAssignment(ctx, endDate, id1, false, nil)
	require.NoError(t, err)

	// Act - get range including all dates
	assignments, err := db.GetAssignmentsByDateRange(ctx, startDate, endDate)

	// Assert
	require.NoError(t, err)
	require.Len(t, assignments, 3)

	// Act - get partial range
	assignments, err = db.GetAssignmentsByDateRange(ctx, startDate, midDate)

	// Assert
	require.NoError(t, err)
	require.Len(t, assignments, 2)
}

func TestCreateBackup_ReturnsSQLiteSnapshot(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	backupBytes, err := db.CreateBackup(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, backupBytes)
	require.True(t, strings.HasPrefix(string(backupBytes), "SQLite format 3\x00"))
}
