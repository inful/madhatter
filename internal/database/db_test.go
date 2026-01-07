package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
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

	// Act
	id, err := db.AddTeamMember("Alice Johnson", "alice@example.com")

	// Assert
	require.NoError(t, err)
	require.NotEmpty(t, id)

	// Verify in database
	members, err := db.GetActiveTeamMembers()
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

	_, err := db.AddTeamMember("Alice", "alice@example.com")
	require.NoError(t, err)

	// Act
	_, err = db.AddTeamMember("Alice 2", "alice@example.com")

	// Assert
	require.Error(t, err)
	require.Contains(t, err.Error(), "UNIQUE")
}

func TestAddTeamMember_EmptyName(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Act
	_, err := db.AddTeamMember("", "alice@example.com")

	// Assert
	require.Error(t, err)
}

func TestGetActiveTeamMembers_Empty(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Act
	members, err := db.GetActiveTeamMembers()

	// Assert
	require.NoError(t, err)
	require.Len(t, members, 0)
}

func TestGetActiveTeamMembers_OnlyActive(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Add active members
	id1, _ := db.AddTeamMember("Alice", "alice@example.com")
	id2, _ := db.AddTeamMember("Bob", "bob@example.com")

	// Deactivate one
	ctx := context.Background()
	_, err := db.ExecContext(ctx, "UPDATE team_members SET is_active = 0 WHERE id = ?", id1)
	require.NoError(t, err)

	// Act
	members, err := db.GetActiveTeamMembers()

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

	_, err := db.AddTeamMember("Alice", "alice@example.com")
	require.NoError(t, err)

	// Act
	member, err := db.GetMemberByEmail("alice@example.com")

	// Assert
	require.NoError(t, err)
	require.Equal(t, "Alice", member.Name)
	require.Equal(t, "alice@example.com", member.Email)
}

func TestGetMemberByEmail_NotFound(t *testing.T) {
	// Arrange
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Act
	_, err := db.GetMemberByEmail("nonexistent@example.com")

	// Assert
	require.Error(t, err)
	require.Equal(t, sql.ErrNoRows, err)
}
