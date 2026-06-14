package api

import (
	"context"
	"net/http"
	"testing"
	"time"

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

func TestWFHAPI_RequestBeyondHorizon_Returns422(t *testing.T) {
	// Set a 90-day horizon so we can easily test the boundary.
	t.Setenv("WFH_REQUEST_HORIZON_DAYS", "90")
	server, cleanup := setupTestServer(t)
	defer cleanup()

	ctx := context.Background()
	sessionToken, err := server.createTestSession(ctx)
	require.NoError(t, err)

	_, err = server.db.AddTeamMember(ctx, "Test User", "test@example.com")
	require.NoError(t, err)

	// Submit a date one day beyond the horizon — tight off-by-one boundary.
	farFuture := nextBusinessDay(time.Now().UTC().AddDate(0, 0, 91))
	api := humatest.Wrap(t, server.api)
	resp := api.Post("/api/v1/wfh", map[string]string{"date": farFuture.Format("2006-01-02")}, "Cookie: session_token="+sessionToken)

	assert.Equal(t, http.StatusUnprocessableEntity, resp.Code)
	assert.Contains(t, resp.Body.String(), "beyond the request horizon", "response body must name the horizon error")
}

// nextBusinessDay returns the next weekday (Mon–Fri) starting from the given time.
func nextBusinessDay(from time.Time) time.Time {
	d := from
	for d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
		d = d.AddDate(0, 0, 1)
	}
	return d
}
