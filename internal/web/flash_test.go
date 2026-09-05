package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSetFlash_BasicPayload pins the redirect contract:
// the helper emits a 303 See Other with a Location URL that
// carries the flash payload as query parameters. Empty fields
// are omitted (the URL has no `?reason=` when Reason is empty).
func TestSetFlash_BasicPayload(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/admin/wfh", nil)

	SetFlash(w, r, "/admin/wfh", Flash{
		Kind:   FlashKindMarkAdminWFH,
		Status: "error",
		Reason: "Please select a member.",
		Member: "alice@example.com",
	})

	require.Equal(t, http.StatusSeeOther, w.Code)
	loc := w.Header().Get("Location")
	assert.True(t, len(loc) > 0 && loc[0] == '/',
		"redirect Location must be an absolute path, got %q", loc)
	assert.Contains(t, loc, "wfh_marked=error",
		"kind must appear in URL with its value, got %q", loc)
	assert.Contains(t, loc, "reason=Please+select+a+member.",
		"reason must be URL-encoded into the query string, got %q", loc)
	assert.Contains(t, loc, "member=",
		"member field must be in the query string, got %q", loc)
}

// TestSetFlash_NoPayloadStillRedirects is the safety-net case:
// SetFlash is called with no Kind. The helper must still redirect
// (callers always set Kind, but a graceful no-op is cheaper than
// a nil deref). The Location must be the bare base path with no
// query string.
func TestSetFlash_NoPayloadStillRedirects(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", nil)

	SetFlash(w, r, "/admin/wfh", Flash{})

	require.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/admin/wfh", w.Header().Get("Location"),
		"empty payload must still redirect to the bare base path")
}

// TestSetFlash_PurgeKindEncodesCountAsValue pins the wire-format
// quirk: the row count is encoded AS the kind value itself
// (?wfh_purged=3), not as a companion ?count=3. This matches
// the pre-existing template at /admin/wfh that reads
// r.URL.Query().Get("wfh_purged") as a count. Refactoring that
// template to use ?count= would be a separate wire-format break.
func TestSetFlash_PurgeKindEncodesCountAsValue(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/admin/wfh", nil)

	SetFlash(w, r, "/admin/wfh", Flash{
		Kind:   FlashKindPurgeWFHPeriods,
		Count:  3,
		Cutoff: "2025-08-31",
	})

	require.Equal(t, http.StatusSeeOther, w.Code)
	loc := w.Header().Get("Location")
	assert.Contains(t, loc, "wfh_purged=3",
		"purge kind encodes count as the value itself, got %q", loc)
	assert.NotContains(t, loc, "count=",
		"purge kind must not also emit a companion ?count=, got %q", loc)
	assert.Contains(t, loc, "cutoff=2025-08-31",
		"cutoff must be URL-encoded, got %q", loc)
}

// TestSetFlash_OmitsEmptyFields pins the "no junk in the URL"
// contract: Reason/Member/Cutoff/StartDate/EndDate that are the
// zero value are not emitted. Templates that switch on the
// presence of these fields can rely on absence meaning "not
// supplied".
func TestSetFlash_OmitsEmptyFields(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", nil)

	SetFlash(w, r, "/team", Flash{
		Kind: FlashKindReportMemberAdded,
	})

	loc := w.Header().Get("Location")
	assert.NotContains(t, loc, "reason=",
		"empty Reason must not be in the URL, got %q", loc)
	assert.NotContains(t, loc, "member=",
		"empty Member must not be in the URL, got %q", loc)
	assert.NotContains(t, loc, "count=",
		"empty Count must not be in the URL, got %q", loc)
	assert.NotContains(t, loc, "start_date=",
		"empty StartDate must not be in the URL, got %q", loc)
	assert.NotContains(t, loc, "end_date=",
		"empty EndDate must not be in the URL, got %q", loc)
}

// TestPopFlash_RoundTrip pins the contract that SetFlash + PopFlash
// are inverses: every field written by SetFlash is read back by
// PopFlash. This is the core symmetry the templates depend on.
func TestPopFlash_RoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   Flash
	}{
		{
			name: "report-today error with reason",
			in: Flash{
				Kind:   FlashKindReportWFHToday,
				Status: "error",
				Reason: "WFH date has already passed.",
			},
		},
		{
			name: "signal-on-site ok (Status defaults)",
			in: Flash{
				Kind: FlashKindSignalOnSiteToday,
			},
		},
		{
			name: "purge with count and cutoff (Status IS the count)",
			in: Flash{
				Kind:   FlashKindPurgeWFHPeriods,
				Count:  7,
				Cutoff: "2025-08-24",
			},
		},
		{
			name: "admin mark ok with member",
			in: Flash{
				Kind:   FlashKindMarkAdminWFH,
				Status: "ok",
				Member: "Alice Anderson",
			},
		},
		{
			name: "leave submitted with period",
			in: Flash{
				Kind:      FlashKindReportLeaveSubmitted,
				StartDate: "2025-09-08",
				EndDate:   "2025-09-12",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Round-trip via a real HTTP round trip: write to a
			// recorder, build a fresh request from the Location
			// header, and PopFlash off that request.
			w := httptest.NewRecorder()
			r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", nil)
			SetFlash(w, r, "/dest", tc.in)

			r2 := httptest.NewRequestWithContext(context.Background(), http.MethodGet, w.Header().Get("Location"), nil)
			out := PopFlash(r2)

			require.NotNil(t, out, "PopFlash must return the flash SetFlash just wrote")
			assert.Equal(t, tc.in.Kind, out.Kind)
			// Status round-trips for kinds where Status is a label
			// (most kinds); for purge, the Status is the count string
			// and the helper resets it after parsing — so we
			// assert that the Count survives, and Status is empty.
			if tc.in.Kind == FlashKindPurgeWFHPeriods {
				assert.Equal(t, tc.in.Count, out.Count)
				assert.Empty(t, out.Status,
					"purge Status is the raw count; PopFlash moves it to Count")
			} else {
				expectedStatus := tc.in.Status
				if expectedStatus == "" {
					// Empty input means "use the kind's default sub-status"
					// (approved for report-today, ok for everything else).
					// SetFlash applied the default on write, so PopFlash
					// must read it back the same way.
					switch tc.in.Kind {
					case FlashKindReportWFHToday:
						expectedStatus = "approved"
					case FlashKindSignalOnSiteToday,
						FlashKindMarkAdminWFH,
						FlashKindReportMemberAdded,
						FlashKindReportLeaveSubmitted,
						FlashKindReportScheduleGenerated,
						FlashKindReportSwapRequested,
						FlashKindReportWFHRequested:
						expectedStatus = "ok"
					case FlashKindPurgeWFHPeriods:
						t.Errorf("purge case is handled in the outer if-branch; switch should not be reached")
					}
				}
				assert.Equal(t, expectedStatus, out.Status)
			}
			assert.Equal(t, tc.in.Reason, out.Reason)
			assert.Equal(t, tc.in.Member, out.Member)
			assert.Equal(t, tc.in.Cutoff, out.Cutoff)
			assert.Equal(t, tc.in.StartDate, out.StartDate)
			assert.Equal(t, tc.in.EndDate, out.EndDate)
		})
	}
}

// TestPopFlash_NoFlashReturnsNil pins the absent-flash contract:
// PopFlash returns nil when the query string carries no known
// kind. Templates that switch on the returned *Flash must
// handle nil.
func TestPopFlash_NoFlashReturnsNil(t *testing.T) {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/dashboard", nil)
	assert.Nil(t, PopFlash(r),
		"PopFlash must return nil when no known kind is present")
}

// TestPopFlash_UnknownKindIgnored pins the forward-compat
// contract: a query string with a kind that doesn't exist (a
// typo, or a kind SetFlash used in the past that PopFlash doesn't
// know about anymore) returns nil rather than returning a partial
// Flash with the unknown value. The URL would still pass through
// to the redirect target; PopFlash just doesn't pick it up.
func TestPopFlash_UnknownKindIgnored(t *testing.T) {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/dashboard?some_typo_key=hi&reason=foo", nil)
	assert.Nil(t, PopFlash(r),
		"PopFlash must return nil for unknown kinds (don't surface half-flashes)")
}

// TestAllFlashKindsCompile guards against the common refactor
// mistake of declaring a FlashKind constant but forgetting to add
// it to allFlashKinds() — the lookup order would silently drop
// the banner at runtime. The test enforces that every const
// appears in the lookup slice.
func TestAllFlashKindsCompile(t *testing.T) {
	lookup := map[FlashKind]bool{}
	for _, k := range allFlashKinds() {
		lookup[k] = true
	}
	declared := map[FlashKind]bool{
		FlashKindReportWFHToday:          true,
		FlashKindSignalOnSiteToday:       true,
		FlashKindPurgeWFHPeriods:         true,
		FlashKindMarkAdminWFH:            true,
		FlashKindReportMemberAdded:       true,
		FlashKindReportLeaveSubmitted:    true,
		FlashKindReportScheduleGenerated: true,
		FlashKindReportSwapRequested:     true,
		FlashKindReportWFHRequested:      true,
	}
	for k := range declared {
		assert.True(t, lookup[k],
			"FlashKind %q is declared as a const but not in allFlashKinds(); PopFlash would silently drop it", k)
	}
}
