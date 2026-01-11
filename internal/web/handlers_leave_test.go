package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/rota"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLeaveCreationIntegration tests the complete leave creation flow.
// This test ensures that leave can be created through the web interface,
// catching issues like missing columns or schema mismatches.
func TestLeaveCreationIntegration(t *testing.T) {
	// Create test database
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	// Add a test team member
	memberID, err := db.AddTeamMember(ctx, "Test User", "test@example.com")
	require.NoError(t, err)

	// Add another member for assignments
	_, err = db.AddTeamMember(ctx, "Test User 2", "test2@example.com")
	require.NoError(t, err)

	// Create mock auth components
	mockAuthManager := &auth.AuthManager{}
	mockMiddleware := &auth.Middleware{}

	// Create maintenance service for schedule handling
	maintenance := rota.NewScheduleMaintenance(db)

	// Create handler
	handler, err := NewHandler(db, mockAuthManager, mockMiddleware, false, nil)
	require.NoError(t, err)
	handler.maintenance = maintenance

	// Prepare form data for leave creation
	formData := url.Values{}
	formData.Set("member_id", memberID)
	formData.Set("start_date", time.Now().Format("2006-01-02"))
	formData.Set("end_date", time.Now().AddDate(0, 0, 5).Format("2006-01-02"))

	// Create POST request
	req := httptest.NewRequest(http.MethodPost, "/leave", strings.NewReader(formData.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// Create response recorder
	w := httptest.NewRecorder()

	// Call the handler
	handler.handleLeaveReport(w, req)

	// Verify response
	assert.Equal(t, http.StatusSeeOther, w.Code, "Should redirect after successful leave creation")
	assert.Equal(t, "/", w.Header().Get("Location"), "Should redirect to dashboard")

	// Verify leave was created in database
	startDate := time.Now().Format("2006-01-02")
	leaveRecords, err := db.GetLeaveByDate(ctx, startDate)
	require.NoError(t, err)
	assert.Len(t, leaveRecords, 1, "Should have created one leave record")
	assert.Equal(t, memberID, leaveRecords[0].MemberID, "Leave should be for the correct member")
}

// TestLeaveCreationWithInvalidData tests error handling for invalid leave data.
func TestLeaveCreationWithInvalidData(t *testing.T) {
	// Create test database
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	// Create mock auth components
	mockAuthManager := &auth.AuthManager{}
	mockMiddleware := &auth.Middleware{}

	// Create handler
	handler, err := NewHandler(db, mockAuthManager, mockMiddleware, false, nil)
	require.NoError(t, err)

	testCases := []struct {
		name           string
		formData       url.Values
		expectedStatus int
	}{
		{
			name: "Missing member ID",
			formData: url.Values{
				"start_date": {time.Now().Format("2006-01-02")},
				"end_date":   {time.Now().AddDate(0, 0, 5).Format("2006-01-02")},
			},
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name: "Missing start date",
			formData: url.Values{
				"member_id": {"test-member-id"},
				"end_date":  {time.Now().AddDate(0, 0, 5).Format("2006-01-02")},
			},
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name: "Missing end date",
			formData: url.Values{
				"member_id":  {"test-member-id"},
				"start_date": {time.Now().Format("2006-01-02")},
			},
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name: "Non-existent member",
			formData: url.Values{
				"member_id":  {"non-existent-id"},
				"start_date": {time.Now().Format("2006-01-02")},
				"end_date":   {time.Now().AddDate(0, 0, 5).Format("2006-01-02")},
			},
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create POST request
			req := httptest.NewRequest(http.MethodPost, "/leave", strings.NewReader(tc.formData.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			// Create response recorder
			w := httptest.NewRecorder()

			// Call the handler
			handler.handleLeaveReport(w, req)

			// Verify error response
			assert.Equal(t, tc.expectedStatus, w.Code, "Should return error status for %s", tc.name)
		})
	}
}

// TestLeaveCreationDatabaseSchema verifies the database schema matches expectations.
// This test helps catch schema migration issues early.
func TestLeaveCreationDatabaseSchema(t *testing.T) {
	// Create test database
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	// Add a test team member
	memberID, err := db.AddTeamMember(ctx, "Test User", "test@example.com")
	require.NoError(t, err)

	// Try to create a leave record directly
	startDate := time.Now().Format("2006-01-02")
	endDate := time.Now().AddDate(0, 0, 5).Format("2006-01-02")

	leaveID, err := db.CreateLeaveRecord(ctx, memberID, startDate, endDate)
	require.NoError(t, err, "Should be able to create leave record without 'type' column")
	require.NotEmpty(t, leaveID, "Should return a valid leave ID")

	// Verify the leave record can be retrieved
	leave, err := db.GetLeaveByID(ctx, leaveID)
	require.NoError(t, err, "Should be able to retrieve created leave record")
	require.NotNil(t, leave, "Retrieved leave should not be nil")
	assert.Equal(t, memberID, leave.MemberID, "Leave should have correct member ID")
}
