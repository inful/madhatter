package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/inful/madhatter/internal/testutil"
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
	farFuture := testutil.NextBusinessDay(time.Now().UTC().AddDate(0, 0, 91))
	api := humatest.Wrap(t, server.api)
	resp := api.Post("/api/v1/wfh", map[string]string{"date": farFuture.Format("2006-01-02")}, "Cookie: session_token="+sessionToken)

	assert.Equal(t, http.StatusUnprocessableEntity, resp.Code)
	assert.Contains(t, resp.Body.String(), "limited number of days in advance", "response body must name the horizon error")
}

// TestWFHAPI_ListExposesOriginField is the Step 19 regression test
// for plans/assigned-wfh-plan.md: the /api/v1/wfh response MUST
// surface each row's Origin so API consumers can distinguish
// self-requested (ad_hoc), system-assigned (assigned), swap-target
// (swap), and recurring WFH rows. A future refactor that drops
// or renames the field would silently break dashboard / calendar
// tools that branch on it.
//
// The wire shape is the source of truth here — the response is
// parsed as raw JSON, not the typed Go struct, so a dropped json
// tag would surface as missing-from-JSON rather than silently
// passing through a Go-zero-value.
func TestWFHAPI_ListExposesOriginField(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	ctx := context.Background()
	sessionToken, err := server.createTestSession(ctx)
	require.NoError(t, err)

	memberID, err := server.db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	// Two rows on different future business days — one with the
	// default empty origin (self-requested shape) and one with
	// Origin="assigned" seeded via raw SQL so the test can pin
	// that non-empty values pass through verbatim, not just
	// structurally present.
	adHocDate := testutil.NextBusinessDay(time.Now().UTC().AddDate(0, 0, 1)).Format("2006-01-02")
	_, err = server.db.CreateWFHRequest(ctx, memberID, adHocDate)
	require.NoError(t, err)

	assignedDate := testutil.NextBusinessDay(time.Now().UTC().AddDate(0, 0, 2)).Format("2006-01-02")
	assignedID, err := server.db.CreateWFHRequest(ctx, memberID, assignedDate)
	require.NoError(t, err)
	// Promote the row to Origin="assigned" via raw SQL — there is
	// no public SetOrigin helper (the column is set by the picker
	// and the swap-accept path, both of which are outside this
	// test's scope). The ExecContext path is the same one
	// CreateAssignedApprovedWFHRequest uses internally.
	_, err = server.db.ExecContext(ctx,
		"UPDATE wfh_requests SET origin = ? WHERE id = ?",
		"assigned", assignedID.ID)
	require.NoError(t, err)

	api := humatest.Wrap(t, server.api)
	resp := api.Get("/api/v1/wfh", "Cookie: session_token="+sessionToken)
	require.Equal(t, http.StatusOK, resp.Code)

	var body struct {
		Requests []map[string]any `json:"requests"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))
	require.Len(t, body.Requests, 2)

	byDate := map[string]map[string]any{}
	for _, r := range body.Requests {
		byDate[r["date"].(string)] = r
	}
	for _, c := range []struct {
		date   string
		origin string
	}{
		{adHocDate, "ad_hoc"},
		{assignedDate, "assigned"},
	} {
		row, ok := byDate[c.date]
		require.True(t, ok, "row for date %s must be present", c.date)
		_, hasOrigin := row["origin"]
		require.True(t, hasOrigin,
			"row for date %s must surface the origin JSON field (got %+v)", c.date, row)
		require.Equal(t, c.origin, row["origin"],
			"row for date %s must surface origin=%q verbatim (got %q)",
			c.date, c.origin, row["origin"])
	}
}
