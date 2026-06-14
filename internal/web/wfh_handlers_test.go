package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/testutil"
	"github.com/inful/madhatter/internal/wfh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleWFHList_MaterializesRecurringRows ensures the list handler
// inserts approved recurring-WFH rows for the current period on first
// load, so the user sees them alongside ad-hoc requests. A second GET
// must be a no-op.
func TestHandleWFHList_MaterializesRecurringRows(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	require.NoError(t, db.SetTeamMemberRecurringWFHDays(ctx, memberID, database.RecurringWFHDays{
		Wednesday: true,
		Thursday:  true,
	}))

	svc := wfh.NewService(db, wfh.Config{
		Enabled:             true,
		MinOnsitePercentage: 50,
		MinOnsiteAbsolute:   1,
		MaxDaysPerPeriod:    2,
		PeriodDays:          7,
		PeriodAnchor:        "2026-01-05",
		SettlementDays:      2,
		WithdrawalHours:     24,
		RequestHorizonDays:  90,
	})
	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)
	h.wfhService = svc

	// First GET: materializer runs, recurring rows appear.
	rec := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/wfh", nil)
	rec = withUser(rec, "alice@example.com", "Alice", false)
	rr := httptest.NewRecorder()
	h.handleWFHList(rr, rec)

	assert.Equal(t, http.StatusOK, rr.Code, "expected 200, body=%s", rr.Body.String())
	rows, err := db.GetWFHRequestsByMember(ctx, memberID)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(rows), 1, "expected at least one recurring row materialized on first GET")
	for _, r := range rows {
		assert.True(t, r.IsRecurring)
		assert.Equal(t, database.WFHStatusApproved, r.Status)
	}

	// Second GET: idempotent — no new rows.
	rec2 := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/wfh", nil)
	rec2 = withUser(rec2, "alice@example.com", "Alice", false)
	rr2 := httptest.NewRecorder()
	h.handleWFHList(rr2, rec2)

	assert.Equal(t, http.StatusOK, rr2.Code)
	rowsAfter, err := db.GetWFHRequestsByMember(ctx, memberID)
	require.NoError(t, err)
	assert.Len(t, rowsAfter, len(rows), "second GET must not insert more rows")
}

// TestHandleWFHRequestPost_BeyondHorizon_RendersError ensures that a POST
// with a date beyond the configured request horizon re-renders the form with
// an error and does not create a row.
func TestHandleWFHRequestPost_BeyondHorizon_RendersError(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	svc := wfh.NewService(db, wfh.Config{
		Enabled:             true,
		MinOnsitePercentage: 50,
		MinOnsiteAbsolute:   1,
		MaxDaysPerPeriod:    2,
		PeriodDays:          7,
		PeriodAnchor:        "2026-01-05",
		SettlementDays:      2,
		WithdrawalHours:     24,
		RequestHorizonDays:  90,
	})
	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)
	h.wfhService = svc

	// Submit a date one day beyond the 90-day horizon — tight off-by-one boundary.
	farFuture := testutil.NextBusinessDay(time.Now().UTC().AddDate(0, 0, 91))
	formBody := "date=" + farFuture.Format("2006-01-02")
	rec := httptest.NewRequestWithContext(ctx, http.MethodPost, "/wfh/request", strings.NewReader(formBody))
	rec.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = withUser(rec, "alice@example.com", "Alice", false)
	rr := httptest.NewRecorder()
	h.handleWFHRequestPost(rr, rec, map[string]any{}, memberID)

	assert.Equal(t, http.StatusOK, rr.Code, "form must re-render on horizon error")
	assert.Contains(t, rr.Body.String(), "90 days in advance", "error message must mention the horizon")

	// Verify no row was created.
	rows, err := db.GetWFHRequestsByMember(ctx, memberID)
	require.NoError(t, err)
	assert.Empty(t, rows, "no WFH row must be created for a beyond-horizon request")
}
