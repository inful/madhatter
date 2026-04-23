package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/inful/madhatter/internal/database/sqlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSwapAPI_CreateSwapWithOwnAssignments_Returns422(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	ctx := context.Background()
	sessionToken, err := server.createTestSession(ctx)
	require.NoError(t, err)

	_, err = server.db.AddTeamMember(ctx, "Test User", "test@example.com")
	require.NoError(t, err)

	member, err := server.db.GetMemberByEmail(ctx, "test@example.com")
	require.NoError(t, err)
	require.NotNil(t, member)

	baseDate := time.Now().AddDate(0, 0, 7)
	a1, err := server.db.CreateRotaAssignment(ctx, baseDate.Format("2006-01-02"), member.ID, false, nil)
	require.NoError(t, err)
	a2, err := server.db.CreateRotaAssignment(ctx, baseDate.AddDate(0, 0, 1).Format("2006-01-02"), member.ID, false, nil)
	require.NoError(t, err)

	api := humatest.Wrap(t, server.api)
	resp := api.Post("/api/v1/swaps",
		map[string]string{
			"requester_assignment_id": a1,
			"target_assignment_id":    a2,
		},
		"Cookie: session_token="+sessionToken,
	)

	assert.Equal(t, http.StatusUnprocessableEntity, resp.Code)
	var body map[string]any
	err = json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)
	assert.Contains(t, body["detail"], "another member")
}

func TestSwapAPI_AcceptPastSwap_Returns409(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	ctx := context.Background()
	queries := server.db.GetQueries()

	_, err := queries.CreateUser(ctx, sqlc.CreateUserParams{
		ID:         "api-bob-user",
		Email:      "bob@example.com",
		Name:       "Bob",
		Provider:   "fake",
		ProviderID: "bob-provider",
		IsAdmin:    sql.NullInt64{Int64: 0, Valid: true},
		IsActive:   sql.NullInt64{Int64: 1, Valid: true},
	})
	require.NoError(t, err)

	aliceID, err := server.db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := server.db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	baseDate := time.Now().AddDate(0, 0, -3)
	aliceAssignmentID, err := server.db.CreateRotaAssignment(ctx, baseDate.Format("2006-01-02"), aliceID, false, nil)
	require.NoError(t, err)
	bobAssignmentID, err := server.db.CreateRotaAssignment(ctx, baseDate.AddDate(0, 0, 1).Format("2006-01-02"), bobID, false, nil)
	require.NoError(t, err)

	swapID, err := server.db.CreateHatSwap(ctx, aliceAssignmentID, bobAssignmentID, aliceID, bobID)
	require.NoError(t, err)

	bobSessionToken, err := server.sessionManager.CreateSession(ctx, "api-bob-user")
	require.NoError(t, err)

	api := humatest.Wrap(t, server.api)
	resp := api.Post("/api/v1/swaps/"+swapID+"/accept", "Cookie: session_token="+bobSessionToken)

	assert.Equal(t, http.StatusConflict, resp.Code)
	var body map[string]any
	err = json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)
	assert.Contains(t, body["detail"], "passed")
}
