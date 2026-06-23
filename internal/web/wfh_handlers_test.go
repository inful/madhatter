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
	"github.com/inful/madhatter/internal/database/sqlc"
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

// -- Past-period purge handler -------------------------------------------------

// newPurgeTestHandler wires up a Handler with the WFH service enabled and
// purge on by default. The test can mutate svc.Config.PurgeEnabled or
// svc.Config.Enabled after this returns.
func newPurgeTestHandler(t *testing.T, db *database.DB, cfg wfh.Config) *Handler {
	t.Helper()
	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)
	if cfg.Enabled || cfg.PurgeEnabled {
		h.wfhService = wfh.NewService(db, cfg)
	}
	return h
}

// seedPurgePastRows inserts rows directly via the SQLC layer so the
// purge handler test has data to preview and delete.
func seedPurgePastRows(t *testing.T, ctx context.Context, db *database.DB, memberID, id, date string) {
	t.Helper()
	_, err := db.GetQueries().CreateWFHRequest(ctx, sqlc.CreateWFHRequestParams{
		ID:       id,
		MemberID: memberID,
		Date:     parseWFHPastDate(t, date),
	})
	require.NoError(t, err)
}

// parseWFHPastDate is the test-local counterpart of the database
// package's parseDate helper.
func parseWFHPastDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	require.NoError(t, err)
	return d
}

// TestHandleWFHPurge_AdminPreview verifies the GET handler renders a
// preview with the cutoff date and the would-delete count.
func TestHandleWFHPurge_AdminPreview(t *testing.T) {
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
		PurgeEnabled:        true,
	})

	// Seed two past rows and one row inside the current period.
	today := time.Now().UTC()
	currentStart, _, err := svc.ComputePeriodBounds(today)
	require.NoError(t, err)
	cutoff := currentStart.AddDate(0, 0, -svc.Config().PeriodDays)

	seedPurgePastRows(t, ctx, db, memberID, "past-1", cutoff.AddDate(0, 0, -10).Format("2006-01-02"))
	seedPurgePastRows(t, ctx, db, memberID, "past-2", cutoff.AddDate(0, 0, -1).Format("2006-01-02"))
	seedPurgePastRows(t, ctx, db, memberID, "current", currentStart.AddDate(0, 0, 1).Format("2006-01-02"))

	h := newPurgeTestHandler(t, db, svc.Config())
	require.True(t, h.wfhService.IsPurgeEnabled())

	rec := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/wfh/purge", nil)
	rec = withUser(rec, "admin@example.com", "Admin", true)
	rr := httptest.NewRecorder()
	h.handleWFHPurge(rr, rec)

	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	body := rr.Body.String()
	assert.Contains(t, body, cutoff.Format("2006-01-02"), "preview must show the cutoff date")
	assert.Contains(t, body, "2", "preview must show the would-delete count of 2")
}

// TestHandleWFHPurge_ConfirmDeletesAndRedirects verifies the POST
// handler deletes past rows and redirects to the admin WFH page with
// the flash query string.
func TestHandleWFHPurge_ConfirmDeletesAndRedirects(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	svc := wfh.NewService(db, wfh.Config{
		Enabled: true, MinOnsitePercentage: 50, MinOnsiteAbsolute: 1,
		MaxDaysPerPeriod: 2, PeriodDays: 7, PeriodAnchor: "2026-01-05",
		SettlementDays: 2, RequestHorizonDays: 90, PurgeEnabled: true,
	})
	today := time.Now().UTC()
	currentStart, _, err := svc.ComputePeriodBounds(today)
	require.NoError(t, err)
	cutoff := currentStart.AddDate(0, 0, -svc.Config().PeriodDays)

	seedPurgePastRows(t, ctx, db, memberID, "past-1", cutoff.AddDate(0, 0, -5).Format("2006-01-02"))
	seedPurgePastRows(t, ctx, db, memberID, "current", currentStart.AddDate(0, 0, 1).Format("2006-01-02"))

	h := newPurgeTestHandler(t, db, svc.Config())

	form := url.Values{"confirm": {"true"}}
	rec := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/wfh/purge", strings.NewReader(form.Encode()))
	rec.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = withUser(rec, "admin@example.com", "Admin", true)
	rr := httptest.NewRecorder()
	h.handleWFHPurge(rr, rec)

	require.Equal(t, http.StatusSeeOther, rr.Code, "must redirect after purge")
	loc := rr.Header().Get("Location")
	assert.True(t, strings.HasPrefix(loc, "/admin/wfh?"), "redirect must carry the flash query string, got %q", loc)
	assert.Contains(t, loc, "wfh_purged=1", "redirect must report the deleted count")
	assert.Contains(t, loc, "cutoff="+cutoff.Format("2006-01-02"))

	// Past row must be gone; current row must survive.
	_, err = db.GetWFHRequestByID(ctx, "past-1")
	require.ErrorIs(t, err, database.ErrWFHNotFound)
	_, err = db.GetWFHRequestByID(ctx, "current")
	require.NoError(t, err)
}

// TestHandleWFHPurge_RejectsWithoutConfirm verifies the POST handler
// refuses to commit without an explicit confirmation.
func TestHandleWFHPurge_RejectsWithoutConfirm(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	svc := wfh.NewService(db, wfh.Config{
		Enabled: true, MinOnsitePercentage: 50, MinOnsiteAbsolute: 1,
		MaxDaysPerPeriod: 2, PeriodDays: 7, PeriodAnchor: "2026-01-05",
		SettlementDays: 2, RequestHorizonDays: 90, PurgeEnabled: true,
	})
	today := time.Now().UTC()
	currentStart, _, err := svc.ComputePeriodBounds(today)
	require.NoError(t, err)
	cutoff := currentStart.AddDate(0, 0, -svc.Config().PeriodDays)
	seedPurgePastRows(t, ctx, db, memberID, "survivor", cutoff.AddDate(0, 0, -5).Format("2006-01-02"))

	h := newPurgeTestHandler(t, db, svc.Config())

	form := url.Values{} // no confirm field
	rec := httptest.NewRequestWithContext(ctx, http.MethodPost, "/admin/wfh/purge", strings.NewReader(form.Encode()))
	rec.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = withUser(rec, "admin@example.com", "Admin", true)
	rr := httptest.NewRecorder()
	h.handleWFHPurge(rr, rec)

	require.Equal(t, http.StatusBadRequest, rr.Code, "must reject POST without confirm")
	_, err = db.GetWFHRequestByID(ctx, "survivor")
	require.NoError(t, err, "row must survive a rejected POST")
}

// TestHandleWFHPurge_DisabledHidesForm verifies that when purge is
// disabled, the GET renders the warning and POST is not even possible
// because the template does not render the form.
func TestHandleWFHPurge_DisabledHidesForm(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	svc := wfh.NewService(db, wfh.Config{
		Enabled: true, MinOnsitePercentage: 50, MinOnsiteAbsolute: 1,
		MaxDaysPerPeriod: 2, PeriodDays: 7, PeriodAnchor: "2026-01-05",
		SettlementDays: 2, RequestHorizonDays: 90, PurgeEnabled: false,
	})
	today := time.Now().UTC()
	currentStart, _, err := svc.ComputePeriodBounds(today)
	require.NoError(t, err)
	cutoff := currentStart.AddDate(0, 0, -svc.Config().PeriodDays)
	seedPurgePastRows(t, ctx, db, memberID, "survivor-disabled", cutoff.AddDate(0, 0, -5).Format("2006-01-02"))

	h := newPurgeTestHandler(t, db, svc.Config())

	rec := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/wfh/purge", nil)
	rec = withUser(rec, "admin@example.com", "Admin", true)
	rr := httptest.NewRecorder()
	h.handleWFHPurge(rr, rec)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), "disabled", "must show a disabled-state message")
	assert.NotContains(t, rr.Body.String(), `name="confirm"`, "form must not render when disabled")
}

// TestHandleWFHPurge_FlashOnAdminPage verifies the GET /admin/wfh
// surfaces the purge confirmation banner when the redirect query
// string carries the flash key.
func TestHandleWFHPurge_FlashOnAdminPage(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)

	rec := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/wfh?wfh_purged=3&cutoff=2025-12-01", nil)
	rec = withUser(rec, "admin@example.com", "Admin", true)
	rr := httptest.NewRecorder()
	h.handleWFHAdminPage(rr, rec)

	require.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	body := rr.Body.String()
	assert.Contains(t, body, "Purged 3 past WFH requests", "banner must render the deleted count")
	assert.Contains(t, body, "2025-12-01", "banner must render the cutoff")
}
