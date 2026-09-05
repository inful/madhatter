package web

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/database/sqlc"
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

	// Create POST request. Inject a session user so the handler's
	// strict auth check (added with the non-admin-coercion fix)
	// doesn't fail this fixture. In production the safeRequireAuth
	// middleware always populates the user before the handler runs;
	// here we simulate the same.
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/leave", strings.NewReader(formData.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &sqlc.GetSessionByTokenRow{
		Email:   "test@example.com",
		Name:    "Test User",
		IsAdmin: sql.NullInt64{Int64: 1, Valid: true},
	}))

	// Create response recorder
	w := httptest.NewRecorder()

	// Call the handler
	handler.handleLeaveReport(w, req)

	// Verify response
	assert.Equal(t, http.StatusSeeOther, w.Code, "Should redirect after successful leave creation")
	assert.True(t, strings.HasPrefix(w.Header().Get("Location"), "/"), "Should redirect to dashboard, got %q", w.Header().Get("Location"))

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
		name             string
		formData         url.Values
		expectedStatus   int
		wantBodyContains string
	}{
		{
			name: "Missing member ID",
			formData: url.Values{
				"start_date": {time.Now().Format("2006-01-02")},
				"end_date":   {time.Now().AddDate(0, 0, 5).Format("2006-01-02")},
			},
			expectedStatus:   http.StatusOK,
			wantBodyContains: "memberID, startDate, and endDate are required",
		},
		{
			name: "Missing start date",
			formData: url.Values{
				"member_id": {"test-member-id"},
				"end_date":  {time.Now().AddDate(0, 0, 5).Format("2006-01-02")},
			},
			expectedStatus:   http.StatusOK,
			wantBodyContains: "invalid start_date format",
		},
		{
			name: "Missing end date",
			formData: url.Values{
				"member_id":  {"test-member-id"},
				"start_date": {time.Now().Format("2006-01-02")},
			},
			expectedStatus:   http.StatusOK,
			wantBodyContains: "invalid end_date format",
		},
		{
			name: "Non-existent member",
			formData: url.Values{
				"member_id":  {"non-existent-id"},
				"start_date": {time.Now().Format("2006-01-02")},
				"end_date":   {time.Now().AddDate(0, 0, 5).Format("2006-01-02")},
			},
			expectedStatus:   http.StatusOK,
			wantBodyContains: "member not found",
		},
		{
			name: "End date before start date",
			formData: url.Values{
				"member_id":  {"test-member-id"},
				"start_date": {time.Now().AddDate(0, 0, 5).Format("2006-01-02")},
				"end_date":   {time.Now().Format("2006-01-02")},
			},
			expectedStatus:   http.StatusOK,
			wantBodyContains: "end_date must be on or after start_date",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create POST request
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/leave", strings.NewReader(tc.formData.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			// Inject an admin session so the handler's strict auth
			// check (added with the non-admin-coercion fix) doesn't
			// fail this fixture. Mirrors what safeRequireAuth does
			// in production.
			req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &sqlc.GetSessionByTokenRow{
				Email:   "test@example.com",
				Name:    "Test User",
				IsAdmin: sql.NullInt64{Int64: 1, Valid: true},
			}))

			// Create response recorder
			w := httptest.NewRecorder()

			// Call the handler
			handler.handleLeaveReport(w, req)

			// Verify error response
			assert.Equal(t, tc.expectedStatus, w.Code, "Should return error status for %s", tc.name)
			if tc.wantBodyContains != "" {
				assert.Contains(t, w.Body.String(), tc.wantBodyContains, "Response body should contain error message")
			}
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

	leaveID, err := db.CreateLeaveRecord(ctx, memberID, startDate, endDate, database.LeaveTypeLeave)
	require.NoError(t, err, "Should be able to create leave record without 'type' column")
	require.NotEmpty(t, leaveID, "Should return a valid leave ID")

	// Verify the leave record can be retrieved
	leave, err := db.GetLeaveByID(ctx, leaveID)
	require.NoError(t, err, "Should be able to retrieve created leave record")
	require.NotNil(t, leave, "Retrieved leave should not be nil")
	assert.Equal(t, memberID, leave.MemberID, "Leave should have correct member ID")
}

// TestHandleLeaveReport_LeaveTypeRoundTrip form-body coverage for the
// new leave_type field. POSTing leave_type=conference must persist on
// the row, and POSTing an invalid value must default to plain leave
// rather than fail the write with an opaque DB error.
func TestHandleLeaveReport_LeaveTypeRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	maintenance := rota.NewScheduleMaintenance(db)

	handler, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)
	handler.maintenance = maintenance

	adminMemberID, err := db.AddTeamMember(ctx, "Admin", "admin@example.com")
	require.NoError(t, err)

	startDate := time.Now().Format("2006-01-02")
	endDate := time.Now().AddDate(0, 0, 1).Format("2006-01-02")

	t.Run("conference persists", func(t *testing.T) {
		form := url.Values{}
		form.Set("member_id", adminMemberID)
		form.Set("start_date", startDate)
		form.Set("end_date", endDate)
		form.Set("leave_type", database.LeaveTypeConference)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/leave/report", strings.NewReader(form.Encode())) //nolint:contextcheck // httptest fixture, not a real context
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &sqlc.GetSessionByTokenRow{ //nolint:contextcheck // context.WithValue wraps an existing context, not a new one
			Email:   "admin@example.com",
			Name:    "Admin",
			IsAdmin: sql.NullInt64{Int64: 1, Valid: true},
		}))
		w := httptest.NewRecorder()
		handler.handleLeaveReport(w, req)

		require.Equal(t, http.StatusSeeOther, w.Code)
		leaves, err := db.GetLeaveRecords(ctx)
		require.NoError(t, err)
		require.Len(t, leaves, 1)
		assert.Equal(t, database.LeaveTypeConference, leaves[0].LeaveType)
	})

	t.Run("invalid value defaults to leave", func(t *testing.T) {
		// Use a different date range so the row is distinct from the
		// previous subtest.
		altStart := time.Now().AddDate(0, 0, 7).Format("2006-01-02")
		altEnd := time.Now().AddDate(0, 0, 8).Format("2006-01-02")

		form := url.Values{}
		form.Set("member_id", adminMemberID)
		form.Set("start_date", altStart)
		form.Set("end_date", altEnd)
		form.Set("leave_type", "bogus-value")

		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/leave/report", strings.NewReader(form.Encode())) //nolint:contextcheck // httptest fixture, not a real context
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &sqlc.GetSessionByTokenRow{ //nolint:contextcheck // context.WithValue wraps an existing context, not a new one
			Email:   "admin@example.com",
			Name:    "Admin",
			IsAdmin: sql.NullInt64{Int64: 1, Valid: true},
		}))
		w := httptest.NewRecorder()
		handler.handleLeaveReport(w, req)

		require.Equal(t, http.StatusSeeOther, w.Code, "body=%s", w.Body.String())
		leaves, err := db.GetLeaveByDate(ctx, altStart)
		require.NoError(t, err)
		require.Len(t, leaves, 1)
		assert.Equal(t, database.LeaveTypeLeave, leaves[0].LeaveType,
			"unknown leave_type must coerce to the default 'leave' value")
	})
}

// TestHandleLeaveEdit_LeaveTypeRoundTrip form-body coverage for the
// edit path: a POSTed leave_type replaces the row's existing value,
// and a missing value preserves the row's existing value (rather
// than resetting to the default).
func TestHandleLeaveEdit_LeaveTypeRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	maintenance := rota.NewScheduleMaintenance(db)

	handler, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)
	handler.maintenance = maintenance

	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	startDate := time.Now().Format("2006-01-02")
	endDate := time.Now().AddDate(0, 0, 1).Format("2006-01-02")

	// Seed a leave that's already typed as conference so we can flip
	// it to plain leave via the edit endpoint.
	leaveID, err := db.CreateLeaveRecord(ctx, memberID, startDate, endDate, database.LeaveTypeConference)
	require.NoError(t, err)

	form := url.Values{}
	form.Set("member_id", memberID)
	form.Set("start_date", startDate)
	form.Set("end_date", endDate)
	form.Set("leave_type", database.LeaveTypeLeave)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/leave/"+leaveID+"/edit", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &sqlc.GetSessionByTokenRow{
		Email:   "alice@example.com",
		Name:    "Alice",
		IsAdmin: sql.NullInt64{Int64: 0, Valid: true},
	}))
	// Wire the chi URL param so handleLeaveEdit can read {id}; the
	// raw-HTTP request must be indistinguishable from one that
	// arrived via the chi router.
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", leaveID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
	w := httptest.NewRecorder()
	handler.handleLeaveEdit(w, req)

	require.Equal(t, http.StatusSeeOther, w.Code, "body=%s", w.Body.String())
	got, err := db.GetLeaveByID(ctx, leaveID)
	require.NoError(t, err)
	assert.Equal(t, database.LeaveTypeLeave, got.LeaveType,
		"a POSTed leave_type must overwrite the row's previous value")
}

// TestHandleLeaveEdit_RawHTTPRejectsLeaveTypeEscalation is the raw-HTTP
// safety net for the new leave_type field. Per AGENTS.md, a non-admin
// posting against someone else's row must produce the right failure
// status AND leave the row unchanged. The test goes one step further:
// it asserts that even the legitimate owner cannot retype a leave
// to a value the DB CHECK constraint would reject (it must coerce
// to the default rather than 5xx), so a misbehaving client doesn't
// bring down the dashboard for the row's owner.
func TestHandleLeaveEdit_RawHTTPRejectsLeaveTypeEscalation(t *testing.T) {
	ctx := context.Background()
	db, err := database.New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	maintenance := rota.NewScheduleMaintenance(db)

	handler, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)
	handler.maintenance = maintenance

	ownerID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	otherID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	startDate := time.Now().Format("2006-01-02")
	endDate := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	leaveID, err := db.CreateLeaveRecord(ctx, ownerID, startDate, endDate, database.LeaveTypeLeave)
	require.NoError(t, err)

	// Non-admin (Bob) tries to edit Alice's leave with a coerced
	// leave_type. The handler's canMutateLeave guard must reject the
	// request with 403; Alice's row must remain at leave_type=leave.
	form := url.Values{}
	form.Set("member_id", otherID)
	form.Set("start_date", startDate)
	form.Set("end_date", endDate)
	form.Set("leave_type", database.LeaveTypeConference)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/leave/"+leaveID+"/edit", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &sqlc.GetSessionByTokenRow{
		Email:   "bob@example.com",
		Name:    "Bob",
		IsAdmin: sql.NullInt64{Int64: 0, Valid: true},
	}))
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", leaveID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
	w := httptest.NewRecorder()
	handler.handleLeaveEdit(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code,
		"non-admin editing someone else's leave must be refused with 403")

	got, err := db.GetLeaveByID(ctx, leaveID)
	require.NoError(t, err)
	assert.Equal(t, ownerID, got.MemberID,
		"member_id must not be re-routed by a tampered form value")
	assert.Equal(t, database.LeaveTypeLeave, got.LeaveType,
		"leave_type must not be re-tagged by a tampered form value")
}
