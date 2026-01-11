package database

import (
	"context"
	"testing"
)

func TestDateDebug(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	memberID, _ := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	leaveID, err := db.CreateLeaveRecord(ctx, memberID, "2024-01-15", "2024-01-15")
	if err != nil {
		t.Fatalf("CreateLeaveRecord failed: %v", err)
	}
	t.Logf("Created leave ID: %s", leaveID)

	leave, err := db.GetLeaveByID(ctx, leaveID)
	if err != nil {
		t.Fatalf("GetLeaveByID failed: %v", err)
	}
	if leave == nil {
		t.Fatal("GetLeaveByID returned nil")
	}
	t.Logf("Leave: %+v", leave)
	t.Logf("StartDate type: %T, value: '%s'", leave.StartDate, leave.StartDate)
	t.Logf("EndDate type: %T, value: '%s'", leave.EndDate, leave.EndDate)
}
