package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWFHAPI_RequestInvalidDate_Returns422(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	ctx := context.Background()
	sessionToken, err := server.createTestSession(ctx)
	require.NoError(t, err)

	_, err = server.db.AddTeamMember(ctx, "Test User", "test@example.com")
	require.NoError(t, err)

	api := humatest.Wrap(t, server.api)
	resp := api.Post("/api/v1/wfh", map[string]string{"date": "not-a-date"}, "Cookie: session_token="+sessionToken)

	assert.Equal(t, http.StatusUnprocessableEntity, resp.Code)
}

func TestWFHAPI_GetByDateInvalidDate_Returns422(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	sessionToken, err := server.createTestSession(context.Background())
	require.NoError(t, err)

	api := humatest.Wrap(t, server.api)
	resp := api.Get("/api/v1/wfh/date/not-a-date", "Cookie: session_token="+sessionToken)

	assert.Equal(t, http.StatusUnprocessableEntity, resp.Code)
}

func TestWFHAPI_ResolveMemberID_DBFailureReturns500(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	ctx := createTestContext(t, server)

	require.NoError(t, server.db.Close())

	_, err := server.resolveWFHMemberID(ctx)
	require.Error(t, err)

	var statusErr huma.StatusError
	require.ErrorAs(t, err, &statusErr)
	assert.Equal(t, http.StatusInternalServerError, statusErr.GetStatus())
}
