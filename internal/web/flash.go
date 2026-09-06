package web

import (
	"net/http"
	"net/url"
	"strconv"
)

// FlashKind names the set of post-redirect banners the app
// surfaces on its pages. Each kind has its own URL query key; the
// template that consumes the flash switches on Kind to render the
// matching Bulma notification. Adding a new banner means adding a
// new constant here, a new entry in allFlashKinds below, and a
// new case in the consuming template's switch.
//
// The kinds are not strictly tied to a single handler — any
// handler can SetFlash any kind — but the canonical vocabulary
// exists so call sites don't invent arbitrary query keys.
type FlashKind string

const (
	// FlashKindReportWFHToday surfaces the outcome of the dashboard
	// "WFH today" Quick Action (same-day unforeseen WFH). The
	// dashboard template renders it above the Today card.
	FlashKindReportWFHToday FlashKind = "wfh_reported"

	// FlashKindSignalOnSiteToday surfaces the outcome of the
	// dashboard "I'm actually coming in today" button (Phase 2).
	// Same consumer as FlashKindReportWFHToday.
	FlashKindSignalOnSiteToday FlashKind = "wfh_signal_on_site"

	// FlashKindPurgeWFHPeriods surfaces the admin purge result on
	// the /admin/wfh page. The Count field carries the number of
	// rows deleted; Cutoff is the cutoff date string. The row count
	// is encoded AS the kind value (?wfh_purged=3) rather than a
	// companion ?count=3 — this matches the existing wire format
	// so the admin page template that already reads ?wfh_purged=N
	// keeps working without changes.
	FlashKindPurgeWFHPeriods FlashKind = "wfh_purged"

	// FlashKindMarkAdminWFH surfaces the admin mark / unmark
	// result on the /admin/wfh page. The Member field carries the
	// affected member's name. The kind value is the sub-status:
	// "ok", "error", "already", "unmarked".
	FlashKindMarkAdminWFH FlashKind = "wfh_marked"

	// FlashKindReportMemberAdded surfaces the success of /team POST
	// (add member) on the team page.
	FlashKindReportMemberAdded FlashKind = "member_added"

	// FlashKindReportLeaveSubmitted surfaces the success of
	// /leave/report POST on the leave-management page. StartDate
	// and EndDate carry the reported period (display only).
	FlashKindReportLeaveSubmitted FlashKind = "leave_submitted"

	// FlashKindReportScheduleGenerated surfaces the success of
	// /schedule/generate POST on the schedule page. Count carries
	// the number of days generated (display only).
	FlashKindReportScheduleGenerated FlashKind = "schedule_generated"

	// FlashKindReportSwapRequested surfaces the success of
	// /swaps POST (create swap request) on the swaps page. Member
	// carries the target member name (display only).
	FlashKindReportSwapRequested FlashKind = "swap_requested"

	// FlashKindReportWFHRequested surfaces the success of
	// /wfh/request POST (submit a WFH request) on the WFH list page.
	// StartDate carries the requested date (display only).
	FlashKindReportWFHRequested FlashKind = "wfh_requested"

	// FlashKindSignalOnSiteFuture surfaces the outcome of the
	// forward-dated "I'll be in on [date]" control on the
	// dashboard (Phase 3 of the on-site override feature). The
	// Date field carries the target date — set on success and on
	// error so the banner can name what was attempted. Mirrors
	// FlashKindSignalOnSiteToday but with a date payload rather
	// than an implicit "today".
	FlashKindSignalOnSiteFuture FlashKind = "wfh_signal_on_site_future"
)

// Flash is the typed payload carried in the post-redirect query
// string. The page template that consumes the flash renders
// based on Kind; Status carries the kind-specific sub-status
// (e.g. "ok" / "error" / "already" / "unmarked"); Reason /
// Member / Count / Cutoff / StartDate / EndDate are kind-specific
// extras, all optional. Only the fields that are set are written
// to the URL — empty fields are omitted so the query string stays
// minimal.
//
// Status is encoded AS the kind value (?wfh_marked=ok, ?wfh_purged=3)
// rather than as a companion key — this matches the wire format
// the existing admin page template reads (r.URL.Query().Get
// ("wfh_marked") / ("wfh_purged")). Refactoring that template to
// read a separate key would be a wire-format break that older
// tabs in the wild would no longer honor.
type Flash struct {
	Kind      FlashKind
	Status    string
	Reason    string
	Member    string
	Count     int64
	Cutoff    string
	StartDate string
	EndDate   string
	// Date is the generic single-date payload carried by the
	// forward-dated on-site override flash. Distinct from
	// StartDate (which carries the start of a date range for
	// leave reports) so each kind renders unambiguously. Set on
	// both success and error so the banner can name the date
	// the user attempted.
	Date string
}

// SetFlash redirects to basePath with the flash payload encoded
// as query parameters. All existing bespoke URL-construction
// (the string concatenation of "?key=val&key=val" in 5+ places)
// routes through this one helper. Empty fields are omitted; the
// redirect always lands on a clean URL the bookmark bar accepts.
//
// For FlashKindPurgeWFHPeriods the row count is encoded AS the
// kind value itself (see FlashKind comment).
func SetFlash(w http.ResponseWriter, r *http.Request, basePath string, f Flash) {
	if f.Kind == "" {
		// No flash payload — just redirect. This shouldn't happen
		// in practice (callers always set Kind), but handling it
		// gracefully is cheaper than a nil-deref.
		http.Redirect(w, r, basePath, http.StatusSeeOther)
		return
	}

	q := url.Values{}
	q.Set(string(f.Kind), kindValueFor(f))
	encodeExtras(&q, f)
	if encoded := q.Encode(); encoded != "" {
		basePath += "?" + encoded
	}
	http.Redirect(w, r, basePath, http.StatusSeeOther)
}

// kindValueFor returns the value that should appear at the kind's
// URL key. For most kinds it's the sub-status (e.g. "ok" /
// "error"); for the purge kind the value IS the row count to
// preserve the existing wire format (?wfh_purged=N).
func kindValueFor(f Flash) string {
	if f.Kind == FlashKindPurgeWFHPeriods && f.Count > 0 {
		return strconv.FormatInt(f.Count, 10)
	}
	if f.Status != "" {
		return f.Status
	}
	// Default sub-status per kind. The templates that consume
	// these expect these values; setting them explicitly would
	// add noise to every call site.
	switch f.Kind {
	case FlashKindReportWFHToday:
		return "approved"
	case FlashKindSignalOnSiteToday,
		FlashKindSignalOnSiteFuture,
		FlashKindMarkAdminWFH,
		FlashKindReportMemberAdded,
		FlashKindReportLeaveSubmitted,
		FlashKindReportScheduleGenerated,
		FlashKindReportSwapRequested,
		FlashKindReportWFHRequested:
		return "ok"
	case FlashKindPurgeWFHPeriods:
		// Purge's Status isn't a label — the row count IS the
		// kind value. kindValueFor returns the count-encoded value
		// before this switch runs, so this case is unreachable.
		return ""
	}
	return ""
}

// encodeExtras writes the optional Flash fields (Reason / Member /
// Cutoff / StartDate / EndDate) into the query. Empty fields are
// omitted so the URL stays minimal. Extracted from SetFlash so
// the cyclomatic complexity of the redirect path stays below the
// linter's cap.
func encodeExtras(q *url.Values, f Flash) {
	if f.Reason != "" {
		q.Set("reason", f.Reason)
	}
	if f.Member != "" {
		q.Set("member", f.Member)
	}
	if f.Cutoff != "" {
		q.Set("cutoff", f.Cutoff)
	}
	if f.StartDate != "" {
		q.Set("start_date", f.StartDate)
	}
	if f.EndDate != "" {
		q.Set("end_date", f.EndDate)
	}
	if f.Date != "" {
		q.Set("date", f.Date)
	}
}

// PopFlash reads and returns the first known flash in the request's
// query string, or nil if none. The page template that consumes
// the flash switches on Kind and renders the matching Bulma
// notification; PopFlash is the read side that turns the query
// string into a typed value.
//
// If multiple kinds are present (unlikely — the redirect URL is
// constructed by a single SetFlash call), the first kind in the
// canonical order wins. Templates should render one banner per
// request; this guard makes that implicit.
func PopFlash(r *http.Request) *Flash {
	for _, kind := range allFlashKinds() {
		raw := r.URL.Query().Get(string(kind))
		if raw == "" {
			continue
		}
		q := r.URL.Query()
		f := &Flash{
			Kind:      kind,
			Status:    raw,
			Reason:    q.Get("reason"),
			Member:    q.Get("member"),
			Cutoff:    q.Get("cutoff"),
			StartDate: q.Get("start_date"),
			EndDate:   q.Get("end_date"),
			Date:      q.Get("date"),
		}
		if kind == FlashKindPurgeWFHPeriods {
			if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
				f.Count = n
				f.Status = "" // status is the count for purge, not a label
			}
		}
		return f
	}
	return nil
}

// allFlashKinds is the canonical lookup order for PopFlash. New
// kinds must be appended; the order only matters when multiple
// kinds appear in one request (unusual). Compile-time check in
// the test file pins the allFlashKinds count and labels.
func allFlashKinds() []FlashKind {
	return []FlashKind{
		FlashKindReportWFHToday,
		FlashKindSignalOnSiteToday,
		FlashKindSignalOnSiteFuture,
		FlashKindPurgeWFHPeriods,
		FlashKindMarkAdminWFH,
		FlashKindReportMemberAdded,
		FlashKindReportLeaveSubmitted,
		FlashKindReportScheduleGenerated,
		FlashKindReportSwapRequested,
		FlashKindReportWFHRequested,
	}
}
