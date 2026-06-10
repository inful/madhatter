package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
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
	})
	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)
	h.wfhService = svc

	// First GET: materializer runs, recurring rows appear.
	rec := httptest.NewRequest(http.MethodGet, "/wfh", nil)
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
	rec2 := httptest.NewRequest(http.MethodGet, "/wfh", nil)
	rec2 = withUser(rec2, "alice@example.com", "Alice", false)
	rr2 := httptest.NewRecorder()
	h.handleWFHList(rr2, rec2)

	assert.Equal(t, http.StatusOK, rr2.Code)
	rowsAfter, err := db.GetWFHRequestsByMember(ctx, memberID)
	require.NoError(t, err)
	assert.Len(t, rowsAfter, len(rows), "second GET must not insert more rows")
}
