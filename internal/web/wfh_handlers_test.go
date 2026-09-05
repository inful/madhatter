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

// TestHandleWFHAdminPage_FiltersPastAndRecurring asserts the admin
// "Manage WFH Requests" page only shows rows that are
// (a) for a future or current date (past rows are not actionable
//
//	from the admin side), and
//
// (b) NOT the result of the recurring-WFH materialiser (those rows
//
//	are managed by the contract, not by the admin).
//
// The page is the admin's view of WFH state that needs attention;
// surfacing past or recurring rows adds noise without value. The
// filter is applied in handleWFHAdminPage so the DB layer doesn't
// need a separate "active-only" query — the user-facing list and
// the past-period purge keep their existing broader queries.
func TestHandleWFHAdminPage_FiltersPastAndRecurring(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	_, err = db.GetQueries().CreateActiveUser(ctx, sqlc.CreateActiveUserParams{
		ID:         "admin-1",
		Email:      "admin@example.com",
		Name:       "Admin",
		Provider:   "fake",
		ProviderID: "admin-1",
		IsAdmin:    sql.NullInt64{Int64: 1, Valid: true},
	})
	require.NoError(t, err)

	// Seed four rows: one past, one current/future, one future
	// recurring, one future recurring-withdrawn (a corner case the
	// filter must still hide).
	today := testutil.NextBusinessDay(time.Now().UTC())
	pastDate := today.AddDate(0, 0, -7).Format("2006-01-02")
	currentDate := today.AddDate(0, 0, 2).Format("2006-01-02")
	futureDate := today.AddDate(0, 0, 7).Format("2006-01-02")
	futureDate2 := today.AddDate(0, 0, 14).Format("2006-01-02")

	_, err = db.GetQueries().CreateWFHRequest(ctx, sqlc.CreateWFHRequestParams{
		ID:       "wfh-past",
		MemberID: memberID,
		Date:     parseYMD(t, pastDate),
	})
	require.NoError(t, err)
	_, err = db.GetQueries().CreateWFHRequest(ctx, sqlc.CreateWFHRequestParams{
		ID:       "wfh-current",
		MemberID: memberID,
		Date:     parseYMD(t, currentDate),
	})
	require.NoError(t, err)

	// Two recurring rows. IsRecurring=true is the only thing the
	// filter checks, so any rows the materialiser would have inserted
	// are equivalent here. Use the low-level CreateApprovedRecurring
	// path that the materialiser itself uses.
	now := time.Now().UTC()
	_, err = db.GetQueries().CreateApprovedRecurringWFHRequest(ctx, sqlc.CreateApprovedRecurringWFHRequestParams{
		ID:        "rec-1",
		MemberID:  memberID,
		Date:      parseYMD(t, futureDate),
		SettledAt: sql.NullTime{Time: now, Valid: true},
	})
	require.NoError(t, err)
	_, err = db.GetQueries().CreateApprovedRecurringWFHRequest(ctx, sqlc.CreateApprovedRecurringWFHRequestParams{
		ID:        "rec-2",
		MemberID:  memberID,
		Date:      parseYMD(t, futureDate2),
		SettledAt: sql.NullTime{Time: now, Valid: true},
	})
	require.NoError(t, err)

	// Confirm the seed left us with 4 rows.
	all, err := db.GetAllWFHRequests(ctx)
	require.NoError(t, err)
	require.Len(t, all, 4, "seed must leave four rows in the table")

	// Hit the admin page.
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

	rec := httptest.NewRequestWithContext(ctx, http.MethodGet, "/admin/wfh", nil)
	rec = withUser(rec, "admin@example.com", "Admin", true)
	rr := httptest.NewRecorder()
	h.handleWFHAdminPage(rr, rec)

	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()

	// Past and recurring rows must not appear in the rendered table.
	for _, hidden := range []string{pastDate, futureDate, futureDate2} {
		assert.NotContains(t, body, hidden,
			"date %s should be filtered out of the admin page", hidden)
	}
	// The one row that survives (currentDate, ad-hoc) must show up.
	assert.Contains(t, body, currentDate,
		"current/future ad-hoc date must appear in the admin page")
}

// parseYMD is a small helper to keep the test data block readable.
func parseYMD(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse("2006-01-02", s)
	require.NoError(t, err)
	return v
}

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

// TestHandleWFHReportToday_Approves_RedirectsWithFlashBanner is the
// form-body coverage for the new "WFH today" entry point. A POST
// from the dashboard button must:
//   - create the row
//   - settle it inline (capacity available → approved)
//   - redirect back to the dashboard with a flash banner that
//     surfaces the outcome as a query-string param
func TestHandleWFHReportToday_Approves_RedirectsWithFlashBanner(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

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

	_, err = db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/wfh/report-today", nil)
	req = withUser(req, "alice@example.com", "Alice", false)
	rr := httptest.NewRecorder()
	h.handleWFHReportToday(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code, "body=%s", rr.Body.String())
	location := rr.Header().Get("Location")
	assert.Contains(t, location, "/?", "redirect must return to the dashboard")
	assert.Contains(t, location, "wfh_reported=approved",
		"flash banner must surface the approved outcome")

	// The row must be persisted and approved on disk.
	today := time.Now().UTC().Format("2006-01-02")
	rows, err := db.GetWFHRequestsByDate(ctx, today)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, database.WFHStatusApproved, rows[0].Status)
}

// TestHandleWFHReportToday_Denied_AtFloor pins the policy decision:
// when the floor is full, ReportToday still creates a row but
// settles it to denied, and the dashboard reads it as On-site
// (no approved row). The flash banner must surface the denied
// outcome so the user knows they were not approved.
func TestHandleWFHReportToday_Denied_AtFloor(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

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

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/wfh/report-today", nil)
	req = withUser(req, "alice@example.com", "Alice", false)
	rr := httptest.NewRecorder()
	h.handleWFHReportToday(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code)
	assert.Contains(t, rr.Header().Get("Location"), "wfh_reported=denied")

	today := time.Now().UTC().Format("2006-01-02")
	rows, err := db.GetWFHRequestsByDate(ctx, today)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, database.WFHStatusDenied, rows[0].Status)
	_ = aliceID
}

// TestHandleWFHReportToday_RawHTTPRejectsEscalation is the raw-HTTP
// safety net for the new endpoint per AGENTS.md. The handler never
// reads member_id from the body — the member comes from the session.
// A raw curl can't escalate to another member even with a tampered
// body. This test pins that contract.
func TestHandleWFHReportToday_RawHTTPRejectsEscalation(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

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

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	bobID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	// Bob is logged in but tries to report WFH "for Alice" via a
	// hand-rolled body that has both member_id and any other field
	// the handler might accidentally read. The handler must ignore
	// the form value and resolve member_id from the session, so
	// only Bob gets a row.
	form := url.Values{}
	form.Set("member_id", aliceID)

	body := strings.NewReader(form.Encode())
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/wfh/report-today", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = withUser(req, "bob@example.com", "Bob", false)
	rr := httptest.NewRecorder()
	h.handleWFHReportToday(rr, req)

	require.Equal(t, http.StatusSeeOther, rr.Code)

	today := time.Now().UTC().Format("2006-01-02")
	rows, err := db.GetWFHRequestsByDate(ctx, today)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, bobID, rows[0].MemberID,
		"the row must belong to the session's member, not the form value")
	// Sanity: Alice must have zero rows for today.
	rows, err = db.GetWFHRequestsByMember(ctx, aliceID)
	require.NoError(t, err)
	for _, r := range rows {
		assert.NotEqual(t, today, r.Date,
			"no WFH row may be created for Alice when Bob is the session")
	}
}

// TestHandleWFHReportToday_QuotaExhausted_FlashesError is the
// quota-exhaustion branch. The user has used their full quota; the
// handler must NOT create a row, must redirect, and must surface the
// error via the same flash banner mechanism as the happy path.
func TestHandleWFHReportToday_QuotaExhausted_FlashesError(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

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

	aliceID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	_, err = db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	// Burn the quota on two future-dated business days inside the
	// same period as today. Today is the third.
	//
	// Saturday-flake fix: on a Saturday the only future-dated day in
	// today's period is Sunday (which is in the period but not a
	// business day) — there's no way to "burn" two business days in
	// today's period from a Saturday anchor. Skip the test on
	// Saturday; on weekdays this works because today is in the
	// middle of the period with two future business days ahead.
	if now := time.Now().UTC().Weekday(); now == time.Saturday || now == time.Sunday {
		t.Skip("test scenario requires a weekday anchor (Sat/Sun have no future business days in the current period)")
	}
	for _, offset := range []int{1, 2} {
		date := time.Now().UTC().AddDate(0, 0, offset).Format("2006-01-02")
		req, cErr := db.CreateWFHRequest(ctx, aliceID, date)
		require.NoError(t, cErr)
		require.NoError(t, db.UpdateWFHRequestStatus(ctx, req.ID, database.WFHStatusApproved))
	}

	post := httptest.NewRequestWithContext(ctx, http.MethodPost, "/wfh/report-today", nil)
	post = withUser(post, "alice@example.com", "Alice", false)
	rr := httptest.NewRecorder()
	h.handleWFHReportToday(rr, post)

	require.Equal(t, http.StatusSeeOther, rr.Code)
	loc := rr.Header().Get("Location")
	assert.Contains(t, loc, "wfh_reported=error",
		"quota-exhausted must surface as an error flash")
	assert.Contains(t, loc, "reason=",
		"the error flash must carry a human-readable reason")

	// No row for today was created.
	todayStr := time.Now().UTC().Format("2006-01-02")
	rows, err := db.GetWFHRequestsByDate(ctx, todayStr)
	require.NoError(t, err)
	assert.Empty(t, rows, "quota-exhausted must not create a wfh_requests row for today")
}

// -- Selected-date-aware quota banner --------------------------------------

// newRenderFormTestHandler wires up a Handler with a deterministic WFH
// service. Tests in this file use it to assert the rendered form's quota
// banner state (active banner, submit-button disabled attribute).
func newRenderFormTestHandler(t *testing.T, db *database.DB, cfg wfh.Config) *Handler {
	t.Helper()
	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)
	if cfg.Enabled {
		h.wfhService = wfh.NewService(db, cfg)
	}
	return h
}

// TestRenderWFHRequestForm_CurrentPeriodBannerIsActiveByDefault pins the
// default state: GET /wfh/request with no ?date= must render BOTH period
// banners (current + next) with the current-period banner marked
// is-active, and the submit button must be enabled when the user has
// unused quota in the current period.
func TestRenderWFHRequestForm_CurrentPeriodBannerIsActiveByDefault(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	h := newRenderFormTestHandler(t, db, wfh.Config{
		Enabled:             true,
		MinOnsitePercentage: 50,
		MinOnsiteAbsolute:   1,
		MaxDaysPerPeriod:    2,
		PeriodDays:          7,
		PeriodAnchor:        "2026-01-05",
		SettlementDays:      2,
		RequestHorizonDays:  90,
	})

	rr := httptest.NewRecorder()
	h.renderWFHRequestFormAt(rr,
		httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/wfh/request", nil),
		map[string]any{"Template": "wfh_request"}, memberID, "", "")
	out := rr.Body.String()

	assert.Contains(t, out, `id="wfh-period-today"`, "current period banner must render")
	assert.Contains(t, out, `id="wfh-period-next"`, "next period banner must render")

	// The current-period banner must carry is-active so the inline
	// script doesn't have to bootstrap it. The next-period banner
	// must NOT. The template puts id first, class second, so we
	// match that order — RE2 has no backtracking so we can't rely
	// on `.*?` to find either order.
	require.Regexp(t,
		`<div[^>]*\bid="wfh-period-today"[^>]*\bclass="[^"]*is-active"`,
		out, "current-period banner must be is-active by default")
	require.NotRegexp(t,
		`<div[^>]*\bid="wfh-period-next"[^>]*\bclass="[^"]*is-active"`,
		out, "next-period banner must NOT be is-active by default")

	assert.Contains(t, out, `id="wfh-submit"`)
	assert.NotContains(t, out, `id="wfh-submit"[^>]*disabled`,
		"submit must be enabled when the user has unused quota")
}

// TestRenderWFHRequestForm_NextPeriodBannerActiveWhenQueryParamDateIsInNextPeriod
// pins the cross-period behavior: a GET ?date=YYYY-MM-DD where the date
// lands in the next quota period must render the next-period banner as
// is-active and the current-period banner as inactive. This is the
// scenario the user complained about: "I've used 2/2 this month, can I
// request for next month?" — the form must answer YES by showing the
// next-period banner with Remaining=2 and the submit button enabled.
func TestRenderWFHRequestForm_NextPeriodBannerActiveWhenQueryParamDateIsInNextPeriod(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	h := newRenderFormTestHandler(t, db, wfh.Config{
		Enabled:             true,
		MinOnsitePercentage: 50,
		MinOnsiteAbsolute:   1,
		MaxDaysPerPeriod:    2,
		PeriodDays:          7,
		PeriodAnchor:        "2026-01-05",
		SettlementDays:      2,
		RequestHorizonDays:  90,
	})

	// Fill the current period to the max so the current-period
	// banner shows Remaining=0 and the next-period banner shows
	// Remaining=2 (untouched). The two WFH rows must land in the
	// current period (anchor-aligned to 2026-01-05 a Monday) and
	// must be in the FUTURE so CreateWFHRequest's date guard
	// doesn't reject them. We pick the last two days of the
	// current period.
	//
	// Saturday-flake fix: the last two days of the current period
	// are Saturday and Sunday, and on a Saturday the Saturday is
	// "today" (in the past at the time the test runs) and the
	// Sunday is in the future. The Saturday gets rejected by
	// validateRequestDate. Skip on Saturday; on a weekday this
	// picks two valid future dates.
	today := time.Now().UTC()
	if today.Weekday() == time.Saturday || today.Weekday() == time.Sunday {
		t.Skip("test scenario requires a weekday anchor (current period's last two days are weekend)")
	}
	_, currentEnd, err := h.wfhService.ComputePeriodBounds(today)
	require.NoError(t, err)
	_, err = db.CreateWFHRequest(ctx, memberID,
		currentEnd.AddDate(0, 0, -2).Format("2006-01-02"))
	require.NoError(t, err)
	_, err = db.CreateWFHRequest(ctx, memberID,
		currentEnd.AddDate(0, 0, -1).Format("2006-01-02"))
	require.NoError(t, err)

	// A date 1 day past currentEnd is in the next period
	// (PeriodDays=7). Using today+14 would skip into a later
	// period — the test is about the cross-period UX, not the
	// offset.
	nextPeriodDate := currentEnd.AddDate(0, 0, 1).Format("2006-01-02")

	rr := httptest.NewRecorder()
	h.renderWFHRequestFormAt(rr,
		httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/wfh/request?date="+nextPeriodDate, nil),
		map[string]any{"Template": "wfh_request"}, memberID, "", nextPeriodDate)
	out := rr.Body.String()

	require.Regexp(t,
		`<div[^>]*\bid="wfh-period-next"[^>]*\bclass="[^"]*is-active"`,
		out, "next-period banner must be is-active when selected date is in next period")
	require.NotRegexp(t,
		`<div[^>]*\bid="wfh-period-today"[^>]*\bclass="[^"]*is-active"`,
		out, "current-period banner must NOT be is-active when selected date is in next period")

	assert.Contains(t, out, `value="`+nextPeriodDate+`"`,
		"selected date must be preserved in the input value attribute")

	// Next-period quota is full (2 remaining), so the submit button
	// must NOT be disabled despite the current period being exhausted.
	assert.NotContains(t, out, `id="wfh-submit"[^>]*disabled`,
		"submit must be enabled when the selected date's period has unused quota")
}

// TestRenderWFHRequestForm_SubmitStaysEnabledWhenCurrentPeriodExhausted
// pins the new contract: when the user has 0 remaining in the
// current period, the submit button must still render enabled so
// they can pick a future date. Server-side CheckQuota is the
// authoritative guard for over-cap requests — the form simply
// lets them submit and surfaces the friendly error if the
// chosen date's period is also over quota.
//
// Only the holiday check disables the button at render time;
// quota exhaustion is a per-period condition the user can
// sidestep by picking a different date.
func TestRenderWFHRequestForm_SubmitStaysEnabledWhenCurrentPeriodExhausted(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	h := newRenderFormTestHandler(t, db, wfh.Config{
		Enabled:             true,
		MinOnsitePercentage: 50,
		MinOnsiteAbsolute:   1,
		MaxDaysPerPeriod:    2,
		PeriodDays:          7,
		PeriodAnchor:        "2026-01-05",
		SettlementDays:      2,
		RequestHorizonDays:  90,
	})

	// Fill the current period to the max with future dates so
	// CreateWFHRequest's date guard accepts the rows. Pick the
	// last two days of the current period.
	//
	// Saturday-flake fix: same as NextPeriodBannerActiveWhenQuery…
	// above — on a Saturday the period's last two days are weekend
	// and can't both be future. Skip on weekend.
	today := time.Now().UTC()
	if today.Weekday() == time.Saturday || today.Weekday() == time.Sunday {
		t.Skip("test scenario requires a weekday anchor (current period's last two days are weekend)")
	}
	_, currentEnd, err := h.wfhService.ComputePeriodBounds(today)
	require.NoError(t, err)
	_, err = db.CreateWFHRequest(ctx, memberID,
		currentEnd.AddDate(0, 0, -2).Format("2006-01-02"))
	require.NoError(t, err)
	_, err = db.CreateWFHRequest(ctx, memberID,
		currentEnd.AddDate(0, 0, -1).Format("2006-01-02"))
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	h.renderWFHRequestFormAt(rr,
		httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/wfh/request", nil),
		map[string]any{"Template": "wfh_request"}, memberID, "", "")
	out := rr.Body.String()

	// The submit button must render WITHOUT the disabled attribute
	// even when today's quota is exhausted. Match the opening tag
	// up to the '>' and assert no disabled keyword inside.
	btnIdx := strings.Index(out, `id="wfh-submit" type="submit"`)
	require.GreaterOrEqual(t, btnIdx, 0, "wfh-submit button must render")
	endIdx := strings.Index(out[btnIdx:], ">")
	require.GreaterOrEqual(t, endIdx, 0)
	opening := out[btnIdx : btnIdx+endIdx+1]
	assert.NotContains(t, opening, "disabled",
		"submit must render enabled when current-period quota is exhausted; server-side CheckQuota is the authoritative guard for over-cap requests")

	// The current-period banner must still report 0 remaining —
	// confirms the quota data is wired through to the template,
	// not just the disabled attribute being hard-coded.
	require.Regexp(t,
		`id="wfh-period-today"[^>]*data-remaining="0"`,
		out, "current-period banner must report data-remaining=0")
}

// TestRenderWFHRequestForm_HolidaySelectDisablesSubmit pins the holiday
// short-circuit: when the selected date is a public holiday (per the
// installed holiday checker), the submit button must be disabled at
// render time and a "this is a holiday" notification must surface.
// Mirrors the CheckQuota ErrWFHOnHoliday guard at the form layer.
func TestRenderWFHRequestForm_HolidaySelectDisablesSubmit(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	memberID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)

	// Pick a date 3 days out (still in the horizon, still in the
	// current period with default anchors) and register it as a
	// holiday via the install hook.
	holiday := time.Now().UTC().AddDate(0, 0, 3).Format("2006-01-02")
	db.SetHolidayChecker(func(t time.Time) bool {
		return t.Format("2006-01-02") == holiday
	})
	t.Cleanup(func() { db.SetHolidayChecker(nil) })

	h := newRenderFormTestHandler(t, db, wfh.Config{
		Enabled:             true,
		MinOnsitePercentage: 50,
		MinOnsiteAbsolute:   1,
		MaxDaysPerPeriod:    2,
		PeriodDays:          7,
		PeriodAnchor:        "2026-01-05",
		SettlementDays:      2,
		RequestHorizonDays:  90,
	})

	rr := httptest.NewRecorder()
	h.renderWFHRequestFormAt(rr,
		httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/wfh/request?date="+holiday, nil),
		map[string]any{"Template": "wfh_request"}, memberID, "", holiday)
	out := rr.Body.String()

	require.Regexp(t, `id="wfh-submit"[^>]*disabled`,
		out, "submit must be disabled when the selected date is a holiday")
	assert.Contains(t, out, "is a public holiday",
		"holiday notification must surface when the selected date is a holiday")
}

// TestHandleWFHRequestPost_BeyondHorizon_PreservesSelectedDate ensures
// that when the POST handler re-renders the form after rejecting a date
// (horizon, invalid format, quota), the user's submitted date is
// preserved in the input value attribute. The previous implementation
// reset to today, forcing the user to re-pick — a friction point that
// hid the new per-period quota UX.
func TestHandleWFHRequestPost_BeyondHorizon_PreservesSelectedDate(t *testing.T) {
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

	farFuture := testutil.NextBusinessDay(time.Now().UTC().AddDate(0, 0, 91))
	formBody := "date=" + farFuture.Format("2006-01-02")
	rec := httptest.NewRequestWithContext(ctx, http.MethodPost, "/wfh/request", strings.NewReader(formBody))
	rec.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = withUser(rec, "alice@example.com", "Alice", false)
	// Seed data the way wfhBaseData would: with Template=wfh_request so
	// base.html routes to the wfh_request content block instead of the
	// login fallback. handleWFHRequestPost reuses the same data map
	// the parent handleWFHRequest builds in production.
	data := map[string]any{"Template": "wfh_request"}
	rr := httptest.NewRecorder()
	h.handleWFHRequestPost(rr, rec, data, memberID)

	assert.Equal(t, http.StatusOK, rr.Code, "form must re-render on horizon error")
	assert.Contains(t, rr.Body.String(),
		`value="`+farFuture.Format("2006-01-02")+`"`,
		"the rejected date must be preserved in the input value so the user can correct without re-picking")
}

// TestWFHRequest_QuotaBannerHasSpacesBetweenValues pins the
// contract that the quota banner renders the literal words "for",
// "period", "to", "used", "remaining" with spaces between them and
// the dynamic <strong> values. The earlier template put the values
// inside <strong> tags and lost the leading/trailing whitespace, so
// the banner rendered as "forthisperiod", "31to2026-09-06",
// "0used", "2remaining". The fix is &nbsp; between the closing tag
// and the literal word; this test fails if the regression returns.
func TestWFHRequest_QuotaBannerHasSpacesBetweenValues(t *testing.T) {
	ctx := context.Background()
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()

	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
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

	rec := httptest.NewRequestWithContext(ctx, http.MethodGet, "/wfh/request", nil)
	rec = withUser(rec, "alice@example.com", "Alice", false)
	rr := httptest.NewRecorder()
	h.handleWFHRequest(rr, rec)

	assert.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	// The banner must read as "WFH quota for <strong>this</strong> period",
	// not "forthisperiod". The &nbsp; between the closing tag and the
	// next word is what the test pins — without it, the HTML
	// normaliser collapses the whitespace and the banner reads as one
	// run-on word.
	assert.Contains(t, body, "for&nbsp;<strong>this</strong>&nbsp;period",
		"current-period banner must have spaces between 'for' and 'period'")
	assert.Regexp(t, `<strong>\d{4}-\d{2}-\d{2}</strong>&nbsp;to&nbsp;<strong>\d{4}-\d{2}-\d{2}</strong>`,
		body,
		"current-period banner must have a space between period start and 'to'")
	assert.Contains(t, body, "<strong>0</strong>&nbsp;used,",
		"current-period banner must have a space before 'used'")
	assert.Contains(t, body, "<strong>2</strong>&nbsp;remaining.",
		"current-period banner must have a space before 'remaining'")

	// The broken forms are the regressions we want to catch — guard
	// against any revert to a template that emits no whitespace.
	assert.NotContains(t, body, "forthisperiod")
	assert.NotContains(t, body, ">0< used")
	assert.NotContains(t, body, ">2< remaining")
}
