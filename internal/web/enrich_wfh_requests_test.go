package web

import (
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/wfh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEnrichWFHRequests pins the contract of the row-enrichment helper
// used by the admin WFH list view. Each request gets a CanWithdraw
// flag computed from its status and date — the admin UI hides/shows
// the Withdraw button based on this flag, so a regression silently
// leaves admins unable to withdraw valid requests (or, worse, lets
// them withdraw requests whose date has already passed).
func TestEnrichWFHRequests(t *testing.T) {
	t.Parallel()

	// Anchor "today" off the wall clock so we can build relative
	// dates without depending on the suite's runtime date. The
	// wfh.Service.CanWithdraw logic also reads time.Now, so passing
	// it future/past dates relative to the wall clock is what the
	// production call site does.
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	future := today.AddDate(0, 0, 5)
	past := today.AddDate(0, 0, -5)

	// Build a minimal wfh.Service. Only CanWithdraw is exercised, and
	// it is pure-time, so an in-memory DB is sufficient and no real
	// settlement work happens.
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()
	svc := wfh.NewService(db, wfh.Config{Enabled: true, SettlementDays: 7, PeriodDays: 7, PeriodAnchor: "2026-01-05", RequestHorizonDays: 90})

	t.Run("EmptyInput", func(t *testing.T) {
		t.Parallel()

		got := enrichWFHRequests(nil, svc)
		assert.Empty(t, got, "nil input produces an empty enriched slice")

		got = enrichWFHRequests([]database.WFHRequest{}, svc)
		assert.Empty(t, got, "empty input produces an empty enriched slice")
	})

	t.Run("NilService", func(t *testing.T) {
		t.Parallel()

		// A nil svc is the documented nil-safe path: every row gets
		// CanWithdraw=false without panicking. Pin this so a future
		// refactor that drops the guard crashes the admin view rather
		// than silently rendering broken.
		requests := []database.WFHRequest{
			{ID: "r1", Status: database.WFHStatusApproved, Date: future.Format("2006-01-02")},
		}
		got := enrichWFHRequests(requests, nil)
		require.Len(t, got, 1)
		assert.False(t, got[0].CanWithdraw, "nil svc must default CanWithdraw to false")
		assert.Equal(t, "r1", got[0].ID, "the underlying request must be preserved")
	})

	t.Run("ApprovedFutureDate", func(t *testing.T) {
		t.Parallel()

		requests := []database.WFHRequest{
			{ID: "future", Status: database.WFHStatusApproved, Date: future.Format("2006-01-02")},
		}
		got := enrichWFHRequests(requests, svc)
		require.Len(t, got, 1)
		assert.True(t, got[0].CanWithdraw, "an approved future request must be withdrawable")
	})

	t.Run("ApprovedToday", func(t *testing.T) {
		t.Parallel()

		// The current day is the boundary: CanWithdraw must return
		// true (the day is "today or later"), per the docstring on
		// CanWithdraw. Pin this so a future refactor that switches
		// the comparison to "date > today" silently breaks today's
		// self-withdraw path.
		requests := []database.WFHRequest{
			{ID: "today", Status: database.WFHStatusApproved, Date: today.Format("2006-01-02")},
		}
		got := enrichWFHRequests(requests, svc)
		require.Len(t, got, 1)
		assert.True(t, got[0].CanWithdraw, "an approved request for today must be withdrawable")
	})

	t.Run("ApprovedPastDate", func(t *testing.T) {
		t.Parallel()

		requests := []database.WFHRequest{
			{ID: "past", Status: database.WFHStatusApproved, Date: past.Format("2006-01-02")},
		}
		got := enrichWFHRequests(requests, svc)
		require.Len(t, got, 1)
		assert.False(t, got[0].CanWithdraw, "an approved past request must NOT be withdrawable")
	})

	t.Run("NonApprovedStatus", func(t *testing.T) {
		t.Parallel()

		// Pending / Denied / Cancelled / Withdrawn all skip the
		// CanWithdraw computation regardless of date — only Approved
		// is eligible. Pin the full status matrix so a future status
		// is added without updating this list, the test catches the
		// gap.
		statuses := []string{
			database.WFHStatusPending,
			database.WFHStatusDenied,
			database.WFHStatusCancelled,
			database.WFHStatusWithdrawn,
		}
		requests := make([]database.WFHRequest, len(statuses))
		for i, s := range statuses {
			requests[i] = database.WFHRequest{ID: s, Status: s, Date: future.Format("2006-01-02")}
		}
		got := enrichWFHRequests(requests, svc)
		require.Len(t, got, len(statuses))
		for _, r := range got {
			assert.False(t, r.CanWithdraw, "status %s must not be withdrawable even on a future date", r.Status)
		}
	})

	t.Run("UnparseableDate", func(t *testing.T) {
		t.Parallel()

		// A garbage Date string must not panic; CanWithdraw stays
		// false because the parse swallows the error and the
		// short-circuit kicks in. This guards against a future
		// refactor that bubbles the parse error up — which would
		// crash the admin list page on any historical data with a
		// malformed date.
		requests := []database.WFHRequest{
			{ID: "garbage", Status: database.WFHStatusApproved, Date: "not-a-date"},
		}
		got := enrichWFHRequests(requests, svc)
		require.Len(t, got, 1)
		assert.False(t, got[0].CanWithdraw, "an unparseable date must default CanWithdraw to false")
	})

	t.Run("PreservesUnderlyingFields", func(t *testing.T) {
		t.Parallel()

		// The enrichment embeds WFHRequest, so every field must
		// pass through unchanged. Pin a representative field
		// (MemberName) so a future refactor that switches the
		// enriched struct to a copy-by-value loses the embedded
		// fields is caught.
		requests := []database.WFHRequest{
			{ID: "preserve", Status: database.WFHStatusApproved, Date: future.Format("2006-01-02"), MemberName: "Alice"},
		}
		got := enrichWFHRequests(requests, svc)
		require.Len(t, got, 1)
		assert.Equal(t, "preserve", got[0].ID)
		assert.Equal(t, database.WFHStatusApproved, got[0].Status)
		assert.Equal(t, "Alice", got[0].MemberName, "embedded WFHRequest fields must be preserved")
	})

	t.Run("MixedStatusesPerRow", func(t *testing.T) {
		t.Parallel()

		// A list with one of each: only the future-approved row
		// surfaces a withdrawable flag, every other row has false.
		requests := []database.WFHRequest{
			{ID: "approved-future", Status: database.WFHStatusApproved, Date: future.Format("2006-01-02")},
			{ID: "approved-past", Status: database.WFHStatusApproved, Date: past.Format("2006-01-02")},
			{ID: "pending-future", Status: database.WFHStatusPending, Date: future.Format("2006-01-02")},
		}
		got := enrichWFHRequests(requests, svc)
		require.Len(t, got, 3)
		assert.True(t, got[0].CanWithdraw, "approved future must be true")
		assert.False(t, got[1].CanWithdraw, "approved past must be false")
		assert.False(t, got[2].CanWithdraw, "pending future must be false")
	})
}
