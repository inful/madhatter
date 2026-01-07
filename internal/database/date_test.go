package database

import (
	"testing"
)

func TestDateDebug(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	memberID, _ := db.AddTeamMember("Alice", "alice@example.com")
	leaveID, _ := db.CreateLeaveRecord(memberID, "sick", "2024-01-15", "2024-01-15")

	leave, _ := db.GetLeaveByID(leaveID)
	t.Logf("Leave: %+v", leave)
	t.Logf("StartDate type: %T, value: '%s'", leave.StartDate, leave.StartDate)
	t.Logf("EndDate type: %T, value: '%s'", leave.EndDate, leave.EndDate)
}
