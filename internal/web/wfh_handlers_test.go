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
// invokes the recurring-WFH materializer on first load without erroring,
// and that a second GET is a no-op for the row count.
//
// The handler materializes the *current* period (a 7-day window anchored
// to a Monday). The materializer silently skips past dates, so on days
// like Friday–Sunday the current period contains no future recurring
// occurrences and 0 rows are inserted — which is the correct production
// behavior, not a bug. The assertions below therefore allow 0 rows on
// the first GET; the materializer-integration is verified separately
// via the service-level tests with a 14-day forward window.
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
		RequestHorizonDays:  90,
	})
	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)
	h.wfhService = svc

	// First GET: handler invokes the materializer for the current period.
	// The row count depends on which day of the week the test runs;
	// any of 0, 1, or 2 are valid production outcomes.
	rec := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/wfh", nil)
	rec = withUser(rec, "alice@example.com", "Alice", false)
	rr := httptest.NewRecorder()
	h.handleWFHList(rr, rec)

	assert.Equal(t, http.StatusOK, rr.Code, "expected 200, body=%s", rr.Body.String())
	rows, err := db.GetWFHRequestsByMember(ctx, memberID)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(rows), 2, "at most 2 rows in a 7-day current period")
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

	// Materializer integration: when called with a forward window that
	// covers at least one future occurrence of each recurring weekday,
	// the materializer inserts rows. This is the property the original
	// test was trying to verify, decoupled from the handler's
	// current-period scoping.
	today := time.Now().UTC()
	inserted, err := svc.EnsureRecurringMaterializedForMember(ctx, memberID, today, today.AddDate(0, 0, 14))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, inserted, 1, "materializer must insert at least one row in a 14-day forward window")
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
