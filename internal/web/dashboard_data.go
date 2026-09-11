package web

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/rota"
)

// currentUserStatusXxx are the strings the dashboard renders for
// each presence state. Kept here (where loadCurrentUserPresenceStatus
// is implemented) rather than in handler.go so the loader file is
// self-contained — adding a new state touches one file.
const (
	currentUserStatusOnLeave    = "On leave"
	currentUserStatusConference = "@conference"
	currentUserStatusWFH        = "WFH"
	currentUserStatusOnSite     = "On-site"

	// signalOnSiteFutureWindowDays bounds the forward-dated
	// "I'll be in on [date]" picker. Matches the WFH feature's
	// rolling settlement horizon (WFH_SETTLEMENT_DAYS, default 7)
	// so the picker can never offer a date outside the window the
	// scheduler is actually maintaining rows for. The value is a
	// hard cap; rendering is gated on whether any row exists in
	// the window — typical usage renders fewer.
	signalOnSiteFutureWindowDays = 14
)

// signalOnSiteFutureOption is one entry in the dashboard's
// forward-dated on-site picker. DateISO is the wire value posted
// to /wfh/on-site; DateDisplay is the user-facing label. The
// IsRecurring flag is surfaced in the label so a member can tell
// at a glance whether they're withdrawing a contractual pattern
// or a one-off ad-hoc row (the underlying effect is identical —
// the row flips to withdrawn and the quota slot is freed — but
// the affordance should be honest about which kind of row it is).
type signalOnSiteFutureOption struct {
	DateISO     string
	DateDisplay string
	IsRecurring bool
}

// sortSignalOnSiteOptions sorts options by DateISO ascending.
// Cheap insertion sort; the list is at most a handful of entries.
func sortSignalOnSiteOptions(opts []signalOnSiteFutureOption) {
	for i := 1; i < len(opts); i++ {
		for j := i; j > 0 && opts[j-1].DateISO > opts[j].DateISO; j-- {
			opts[j-1], opts[j] = opts[j], opts[j-1]
		}
	}
}

// loadPendingSwapCount fills data["PendingSwapCount"] with the count
// of pending swaps for the logged-in user. Surfaced on the dashboard
// header so a member can see at a glance whether they have a swap
// decision waiting.
func (h *Handler) loadPendingSwapCount(ctx context.Context, data map[string]any) {
	user, ok := auth.GetUserFromContext(ctx)
	if !ok {
		return
	}
	member, mErr := h.db.GetMemberByEmail(ctx, user.Email)
	if mErr != nil || member == nil {
		return
	}
	swaps, sErr := h.db.GetPendingSwapsForMember(ctx, member.ID)
	if sErr != nil {
		return
	}
	data["PendingSwapCount"] = len(swaps)
}

// loadCurrentUserPresenceStatus sets the dashboard's
// CurrentUserPresenceStatus / CurrentUserHasHATDay / WFH quota /
// "next HAT/WFH/leave" fields. The status string is one of the
// currentUserStatusXxx constants and drives the template's CSS
// selector.
//
//nolint:cyclop // Presence status checks multiple independent data sources.
func (h *Handler) loadCurrentUserPresenceStatus(ctx context.Context, data map[string]any, email string) {
	member, err := h.db.GetMemberByEmail(ctx, email)
	if err != nil || member == nil {
		return
	}

	if h.wfhService != nil {
		quota, quotaErr := h.wfhService.GetQuotaStatus(ctx, member.ID)
		if quotaErr == nil {
			data["CurrentUserWFHQuota"] = quota
			data["CurrentUserWFHQuotaExhausted"] = quota.Remaining <= 0
		}
	}

	today := time.Now().Format("2006-01-02")

	// Build the set of dates (>= today) the user is on leave for. This is
	// reused below to gate the HAT-day badge, the Next HAT day, and the
	// Next Leave day, so the "actual" HAT day reflects reassignments.
	leaveDates := h.buildUserLeaveDates(ctx, member.ID, today)

	// HAT day badge: true if the user is on HAT duty today, either as the
	// primary assignee or as a cover for someone else. A leave day hides
	// the badge even when the user has a cover assignment, because an
	// on-leave user is not actually on call - any cover assignment they
	// hold on a leave day is stale data the engine would have reassigned.
	assignments, err := h.db.GetAssignmentsByDate(ctx, today)
	if err == nil {
		for i := range assignments {
			if assignments[i].MemberID != member.ID {
				continue
			}
			if _, onLeave := leaveDates[today]; onLeave {
				break
			}
			data["CurrentUserHasHATDay"] = true
			break
		}
	}

	// CanSignalOnSiteToday: the dashboard "I'm actually coming in
	// today" button is shown when the user has an approved WFH
	// row today that they could self-withdraw. The button is hidden
	// when there's nothing to override (avoids a flash error on
	// click). Ad-hoc and recurring rows both qualify; system-
	// assigned rows don't (they need a swap, which the handler
	// rejects with ErrWFHAssigned).
	//
	// CanSignalOnSiteFuture: Phase 3 sibling for the forward-dated
	// "I'll be in on [date]" control. Lists future approved WFH
	// rows within the rolling settlement window so the dashboard
	// can render a date-picker constrained to dates the user
	// actually has a row for — submitting a date with no row would
	// surface a flash banner, but a constrained picker avoids the
	// round trip. Rows from system-assigned origins are excluded
	// for the same reason as CanSignalOnSiteToday.
	h.loadSignalOnSiteOptions(ctx, data, member.ID, today)

	h.loadCurrentUserUpcomingDates(ctx, data, member.ID, today, leaveDates)

	if _, onLeave := leaveDates[today]; onLeave {
		data["CurrentUserPresenceStatus"] = currentUserStatusOnLeave
		// Tagged conference leaves surface as "@conference" on the
		// Today badge so the user can tell at a glance that they're
		// away for a different reason than a plain day off.
		if isConferenceLeaveToday(ctx, h.db, member.ID, today) {
			data["CurrentUserPresenceStatus"] = currentUserStatusConference
		}
		return
	}

	allWFHRequests, wfhErr := h.db.GetWFHRequestsByDate(ctx, today)
	if wfhErr == nil {
		for i := range allWFHRequests {
			if allWFHRequests[i].MemberID != member.ID {
				continue
			}
			if allWFHRequests[i].Status == database.WFHStatusApproved {
				data["CurrentUserPresenceStatus"] = currentUserStatusWFH
				data["IsAdminMarkedWFH"] = allWFHRequests[i].IsAdminMarked
				return
			}
			// Explicit non-approved row (withdrawn, denied, cancelled,
			// pending). Honor the decision over the contractual pattern.
			data["CurrentUserPresenceStatus"] = currentUserStatusOnSite
			return
		}
	}

	// No row exists for today. Project the contractual recurring
	// weekday forward — the materialiser will create an approved row on
	// its next run. This is the only path where the recurring weekday
	// drives the answer.
	if member.IsRecurringWFHOn(time.Now()) {
		data["CurrentUserPresenceStatus"] = currentUserStatusWFH
		return
	}

	data["CurrentUserPresenceStatus"] = currentUserStatusOnSite
}

// buildUserLeaveDates returns the set of dates (>= today) on which the
// given member is on an active leave. Only "pending" or "assigned"
// leaves are included — same filter the rota engine applies.
func (h *Handler) buildUserLeaveDates(ctx context.Context, memberID, today string) map[string]struct{} {
	leaveDates := make(map[string]struct{})

	todayTime, err := time.Parse("2006-01-02", today)
	if err != nil {
		return leaveDates
	}

	leaveRecords, err := h.db.GetLeaveRecords(ctx)
	if err != nil {
		return leaveDates
	}

	for i := range leaveRecords {
		if leaveRecords[i].MemberID != memberID {
			continue
		}

		status := strings.ToLower(strings.TrimSpace(leaveRecords[i].Status))
		if status != "pending" && status != "assigned" {
			continue
		}

		endDate := leaveRecords[i].EndDate
		if endDate.Before(todayTime) {
			continue
		}

		startDate := leaveRecords[i].StartDate
		if startDate.Before(todayTime) {
			startDate = todayTime
		}

		for d := startDate; !d.After(endDate); d = d.AddDate(0, 0, 1) {
			leaveDates[d.Format("2006-01-02")] = struct{}{}
		}
	}

	return leaveDates
}

// loadCurrentUserUpcomingDates fills "next HAT / next WFH / next
// leave" fields used in the dashboard's right-rail summary card.
//
//nolint:cyclop // Upcoming date resolution combines multiple domain queries.
func (h *Handler) loadCurrentUserUpcomingDates(ctx context.Context, data map[string]any, memberID, today string, leaveDates map[string]struct{}) {
	futureAssignments, err := h.db.GetFutureAssignmentsForMember(ctx, memberID)
	if err == nil {
		for i := range futureAssignments {
			if _, onLeave := leaveDates[futureAssignments[i].Date]; onLeave {
				continue
			}
			data["CurrentUserNextHATDay"] = futureAssignments[i].Date
			break
		}
	}

	wfhRequests, err := h.db.GetWFHRequestsByMember(ctx, memberID)
	if err == nil {
		nextWFHDay := ""
		for i := range wfhRequests {
			if wfhRequests[i].Date < today {
				continue
			}
			if wfhRequests[i].Status != database.WFHStatusPending && wfhRequests[i].Status != database.WFHStatusApproved {
				continue
			}
			if nextWFHDay == "" || wfhRequests[i].Date < nextWFHDay {
				nextWFHDay = wfhRequests[i].Date
			}
		}

		if nextWFHDay != "" {
			data["CurrentUserNextWFHDay"] = nextWFHDay
		}
	}

	if len(leaveDates) > 0 {
		nextLeaveDay := ""
		for date := range leaveDates {
			if nextLeaveDay == "" || date < nextLeaveDay {
				nextLeaveDay = date
			}
		}
		if nextLeaveDay != "" {
			data["CurrentUserNextLeaveDay"] = nextLeaveDay
		}
	}
}

// loadSignalOnSiteOptions populates the data map with the
// dashboard's on-site override affordances:
//
//   - CanSignalOnSiteToday       bool — today-button visibility
//   - CanSignalOnSiteFuture      bool — future-dated picker visibility
//   - SignalOnSiteFutureOptions  []signalOnSiteFutureOption — picker choices
//
// The today button is shown when the user has an approved WFH row
// today that they could self-withdraw (recurring or ad-hoc; not
// system-assigned). The future-dated picker is shown when the user
// has at least one future approved WFH row in the rolling settlement
// window (signalOnSiteFutureWindowDays). Both visibility flags are
// hidden when nothing is overridable so a click can never surface a
// flash error.
//
// System-assigned and swap-origin rows are excluded from both
// affordances — those need a swap, which the on-site override
// path explicitly rejects with ErrWFHAssigned. See
// internal/wfh/service.go SignalOnSiteToday for the rejection
// reason; the dashboard's exclusion keeps the click affordance
// honest about what the user can do.
func (h *Handler) loadSignalOnSiteOptions(ctx context.Context, data map[string]any, memberID, today string) {
	rows, err := h.db.GetWFHRequestsByMember(ctx, memberID)
	if err != nil {
		return
	}

	cutoff := time.Now().UTC().AddDate(0, 0, signalOnSiteFutureWindowDays).Format("2006-01-02")
	futureOptions := make([]signalOnSiteFutureOption, 0, len(rows))
	for i := range rows {
		opt, include := signalOnSiteOptionForRow(&rows[i], today, cutoff)
		if !include {
			continue
		}
		if opt == nil {
			// Today match — the per-row helper flagged the today
			// button visibility; nothing to add to futureOptions.
			data["CanSignalOnSiteToday"] = true
			continue
		}
		futureOptions = append(futureOptions, *opt)
	}
	// Stable order: nearest first. Cheap insertion sort; the list
	// is at most a handful of entries.
	sortSignalOnSiteOptions(futureOptions)
	if len(futureOptions) > 0 {
		data["CanSignalOnSiteFuture"] = true
		data["SignalOnSiteFutureOptions"] = futureOptions
	}
}

// signalOnSiteOptionForRow classifies one WFH row into either:
//   - (nil, true) — the row matches today and the today-button should render
//   - (option, true) — the row is a future-dated candidate for the picker
//   - (nil, false) — the row should be skipped (wrong origin/status/date
//     or unparseable)
//
// Extracted from loadSignalOnSiteOptions so the orchestrator stays
// under the cyclomatic-complexity cap.
func signalOnSiteOptionForRow(r *database.WFHRequest, today, cutoff string) (*signalOnSiteFutureOption, bool) {
	if r.Origin == "assigned" || r.Origin == "swap" {
		return nil, false
	}
	if r.Status != database.WFHStatusApproved {
		return nil, false
	}
	if r.Date == today {
		// Today match — caller flips the today-button flag.
		return nil, true
	}
	if r.Date < today || r.Date > cutoff {
		return nil, false
	}
	parsed, parseErr := time.Parse("2006-01-02", r.Date)
	if parseErr != nil {
		return nil, false
	}
	return &signalOnSiteFutureOption{
		DateISO:     r.Date,
		DateDisplay: parsed.Format("Mon, Jan 2"),
		IsRecurring: r.IsRecurring,
	}, true
}

// todayFullTeamOnSite probes the data map's ScheduleMatrix for a
// today-flagged column whose FullTeamOnSite is true. Returns false
// when the matrix is missing or has no today column. The matrix is
// built in loadDashboardData before loadTodayContext runs, so the
// read here is always against the freshly-built structure.
//
// The reflection-free type assertion keeps loadTodayContext free of
// a scheduleMatrix import cycle (the helper lives in the same
// package as the matrix builder, so a plain type assertion is fine).
func todayFullTeamOnSite(data map[string]any) bool {
	matrix, ok := data["ScheduleMatrix"].(scheduleMatrix)
	if !ok {
		return false
	}
	for i := range matrix.Days {
		if matrix.Days[i].IsToday && matrix.Days[i].FullTeamOnSite {
			return true
		}
	}
	return false
}

// todayNoOneWFH probes the data map's ScheduleMatrix for a
// today-flagged column whose WFHCount is zero AND AtWorkCount
// is non-zero. The two-count test is the contract: WFHCount == 0
// alone is the "no one is WFH" condition, but AtWorkCount > 0
// is the "someone is actually at work" guard that prevents
// the marker from firing on an empty-matrix day (where both
// counts are trivially zero) or a day where every active
// member is on leave (where AtWorkCount is also zero and
// "no one is WFH" doesn't imply "everyone at work is in the
// office" — there IS no one at work).
//
// Conceptually this is the "good day for in-person meetings"
// state: nobody is remote, so anyone who turns up at the
// office today is physically present. The marker is a sibling
// of TodayFullTeamOnSite but stricter-axis-orthogonal — a full-
// team day is also a no-one-WFH day, but a day with people on
// leave (and therefore WFHCount == 0 + AtWorkCount >= 1) is
// a no-one-WFH day without being a full-team day. The template
// renders the full-team banner in preference to this one when
// both apply.
func todayNoOneWFH(data map[string]any) bool {
	matrix, ok := data["ScheduleMatrix"].(scheduleMatrix)
	if !ok {
		return false
	}
	for i := range matrix.Days {
		if matrix.Days[i].IsToday &&
			matrix.Days[i].WFHCount == 0 &&
			matrix.Days[i].AtWorkCount > 0 {
			return true
		}
	}
	return false
}

// autumnalEquinoxDay is the calendar day the dashboard treats as
// the first day of autumn in the Northern Hemisphere. The exact
// astronomical equinox drifts between September 22 and 23 year to
// year; September 23 is the conventional "first day of autumn"
// date in the user-facing calendar and is fixed here so the
// effect is reproducible rather than ephemeris-driven. Users in
// the Southern Hemisphere get the wrong season but the effect
// still fires — a deliberate scope cut documented in the README.
const autumnalEquinoxDay = 23

// funEffectsFor resolves the trigger flags for the dashboard's
// optional fun effects. Returns (confetti, snow, leaves) booleans
// that drive the vendored canvas-confetti bundle:
//
//   - Confetti fires on a business day when the entire team is
//     on-site (the same condition that drives the existing
//     .full-team-banner). Suppressing it on weekends and holidays
//     mirrors the banner's TodayIsBusinessDay gate — neither
//     effect should run on a day with no team in the office.
//
//   - Snow fires whenever the date's month is December. The
//     operator-level SNOW_ENABLED gate is read at Handler
//     construction (see NewHandler); this helper is the per-request
//     "should this fire on this date" decision only.
//
//   - Leaves fire on September 23rd (the first day of autumn)
//     when the full team is on-site on a business day. The
//     business-day gate is what excludes a Sept 23 that lands on
//     a weekend — the matrix would have no rows for a weekend
//     day, so the FullTeamOnSite flag is structurally false and
//     the effect wouldn't fire anyway, but the explicit
//     isBusinessDay guard makes the contract readable.
//
// The signature is intentionally pure (takes all six inputs as
// parameters) so the test cases can pin every branch without
// faking a Handler or a database connection.
// funEffectsFor resolves every celebratory / seasonal effect
// gate for the dashboard. The return tuple is (confetti, snow,
// leaves, birthday):
//
//   - confetti: full-team-on-site burst (#59). Triggered when
//     every active member is on-site on a normal business day.
//   - snow: December falling snow. Calendar window only —
//     fires regardless of presence.
//   - leaves: autumnal-equinox falling leaves. Calendar
//     window AND full-team-on-site piggyback.
//   - birthday (#60 follow-up): celebratory birthday burst.
//     Triggered on the exact day of a member's birthday
//     (DaysUntil=0) on a business day. The banner uses the
//     inclusive 7-day window, but the BLAST only fires on
//     the exact day — a birthday "this week" copy is calmer
//     and the right register for a future date.
//
// All four gates are operator-disableable via env vars
// resolved at Handler construction (CONFETTI_ENABLED,
// SNOW_ENABLED, LEAVES_ENABLED, BIRTHDAY_CONFETTI_ENABLED).
// The reduced-motion OS preference is respected at the
// JS-effect site, not here — the data map only carries the
// intent; the vendored canvas-confetti and our snow/leaves
// storms opt out at runtime.
func funEffectsFor(
	now time.Time,
	fullTeamOnSite, isBusinessDay, birthdayToday bool,
	confettiEnabled, snowEnabled, leavesEnabled, birthdayEnabled bool,
) (confetti, snow, leaves, birthday bool) {
	confetti = confettiEnabled && fullTeamOnSite && isBusinessDay
	snow = snowEnabled && now.Month() == time.December
	leaves = leavesEnabled && now.Month() == time.September && now.Day() == autumnalEquinoxDay && fullTeamOnSite && isBusinessDay
	birthday = birthdayEnabled && birthdayToday && isBusinessDay
	return confetti, snow, leaves, birthday
}

// hasBirthdayToday reports whether the dashboard probe has
// loaded a member whose birthday lands exactly today
// (DaysUntil=0). The blast only fires on the same day; the
// banner uses a wider 7-day window, so this probe is a
// stricter subset. Empty / nil data and probe errors map to
// false so the dashboard still renders cleanly when the
// birthday probe hasn't run yet.
func hasBirthdayToday(data map[string]any) bool {
	bdays, ok := data["UpcomingBirthdays"].([]database.UpcomingBirthday)
	if !ok || len(bdays) == 0 {
		return false
	}
	return bdays[0].DaysUntil == 0
}

// loadDashboardData populates the dashboard with today's and week's
// assignments. The orchestrator (handleDashboard) calls this after
// the schedule is ensured so presence snapshots are stable.
// loadTodayContext populates the dashboard's "today" classification:
// whether today is a weekend, a holiday, or a normal business day, the
// holiday name (if any), and the next business day on the rota. The
// dashboard uses this to surface weekend / holiday status instead of
// pretending today is a normal weekday (the earlier behavior — a
// plain "Today / On-site" badge on a Sunday — confused operators).
//
// Extracted from loadDashboardData so the orchestrator stays under
// the cyclomatic-complexity cap.
func (h *Handler) loadTodayContext(now time.Time, data map[string]any) {
	isWeekend := now.Weekday() == time.Saturday || now.Weekday() == time.Sunday
	isHoliday := h.holidayChecker != nil && h.holidayChecker(now)

	data["TodayIsWeekend"] = isWeekend
	data["TodayIsHoliday"] = isHoliday
	data["TodayIsBusinessDay"] = !isWeekend && !isHoliday

	// Fun effects (issue #59 + #60). confettiEnabled /
	// snowEnabled / leavesEnabled / birthdayConfettiEnabled were
	// resolved once at Handler construction from
	// CONFETTI_ENABLED / SNOW_ENABLED / LEAVES_ENABLED /
	// BIRTHDAY_CONFETTI_ENABLED (defaults true). The per-request
	// work here is just the date-and-context check —
	// fullTeamOnSite probes the already-built ScheduleMatrix, the
	// snow / leaves tests are single month comparisons, and
	// birthdayToday probes the already-loaded UpcomingBirthdays
	// slice for an exact-day match. The dashboard template turns
	// the booleans into a hidden <div> with data-confetti /
	// data-snow / data-leaves / data-birthday-confetti
	// attributes that fun-effects.js reads.
	confetti, snow, leaves, birthday := funEffectsFor(
		now,
		todayFullTeamOnSite(data),
		data["TodayIsBusinessDay"].(bool),
		hasBirthdayToday(data),
		h.confettiEnabled,
		h.snowEnabled,
		h.leavesEnabled,
		h.birthdayConfettiEnabled,
	)
	data["FunEffectsConfetti"] = confetti
	data["FunEffectsSnow"] = snow
	data["FunEffectsLeaves"] = leaves
	data["FunEffectsBirthday"] = birthday

	if isHoliday && h.holidayLookup != nil {
		if name, ok := h.holidayLookup.GetHoliday(now.Format("2006-01-02")); ok {
			data["TodayHolidayName"] = name
		}
	}

	if isWeekend || isHoliday {
		if next := nextBusinessDayFrom(now, h.isBusinessDay); !next.IsZero() {
			data["NextBusinessDayDisplay"] = next.Format("Monday, Jan 2")
			data["NextBusinessDayISO"] = next.Format("2006-01-02")
		}
	}

	// Probe the schedule matrix for today's FullTeamOnSite flag.
	// buildScheduleMatrix runs in loadDashboardData before this
	// function fires, so the matrix is already in the data map
	// when we look. The presence probe is a defensive default —
	// when the matrix didn't load (db error or empty presence),
	// the banner stays hidden rather than guessing.
	data["TodayFullTeamOnSite"] = todayFullTeamOnSite(data)

	// Sibling probe: today has zero WFH rows AND at least one
	// person actually at work. The dashboard renders this as
	// "No one is WFH today" — the practical cue that today is
	// a good day for an in-person meeting (the people who are
	// working today are all in the office). Mutually exclusive
	// with TodayFullTeamOnSite in the template (the full-team
	// banner wins when both apply), so the visual hierarchy
	// stays clean.
	data["TodayNoOneWFH"] = todayNoOneWFH(data)
}

// nextBusinessDayFrom walks forward day-by-day from start until
// isBusinessDay returns true. Returns the zero time if the walker
// can't find one within the safety cap (1 year). Pure function —
// takes the gate as a parameter — so the helper stays testable
// without wiring a Handler.
func nextBusinessDayFrom(start time.Time, isBusinessDay func(time.Time) bool) time.Time {
	const safetyCap = 366
	d := start.AddDate(0, 0, 1)
	for range safetyCap {
		if isBusinessDay(d) {
			return d
		}
		d = d.AddDate(0, 0, 1)
	}
	return time.Time{}
}

func (h *Handler) loadDashboardData(ctx context.Context, data map[string]any) {
	now := time.Now()
	today := now.Format("2006-01-02")
	// Today is exposed both as data["Today"] (for the
	// dashboard template's `<meta name="current-day">`
	// tag) and as the canonical date the schedule matrix,
	// leaves, and birthday probes read. Single source of
	// truth: every other "today" derivation in the dashboard
	// reads from this value, so a future date-format change
	// is a single edit.
	data["Today"] = today
	assignments, err := h.db.GetAssignmentsByDate(ctx, today)
	if err == nil && len(assignments) > 0 {
		data["TodayAssignment"] = assignments[0]
	}

	if presence, presenceErr := h.getUpcomingPresence(ctx); presenceErr == nil {
		data["UpcomingPresence"] = presence
		data["ScheduleMatrix"] = buildScheduleMatrix(presence, h.wfhFloor(ctx))
	}

	weeksData, err := h.getFullWeeks(ctx)
	if err == nil {
		data["CurrentWeek"] = h.buildWeekData(weeksData, true)
		data["NextWeek"] = h.buildWeekData(weeksData, false)
	}

	if h.holidayChecker != nil {
		data["UpcomingHolidays"] = h.getUpcomingHolidays()
	}

	h.loadMeetingsToken(ctx, data)

	// Birthday probe (#60). The DB query is the cheapest
	// possible read of "active members with a birthdate"; the
	// window filter (today..today+7, inclusive) and the
	// year-wrap math run in db.GetUpcomingBirthdays. A probe
	// error here must NOT fail the whole dashboard load —
	// the birthday banner is nice-to-have, not load-bearing
	// for the page to function. Log it and surface an empty
	// slice so the template renders nothing.
	//
	// The probe MUST run before loadTodayContext because
	// loadTodayContext calls funEffectsFor, which reads
	// data["UpcomingBirthdays"][0].DaysUntil == 0 to compute
	// the data-birthday-confetti attribute. If the probe ran
	// after loadTodayContext, the confetti blast would never
	// fire for the birthday kid.
	const birthdayWindowDays = 7
	if birthdays, bdayErr := h.db.GetUpcomingBirthdays(ctx, now, birthdayWindowDays); bdayErr != nil {
		slog.WarnContext(ctx, "birthday probe failed", "error", bdayErr)
		data["UpcomingBirthdays"] = []database.UpcomingBirthday{}
	} else {
		data["UpcomingBirthdays"] = birthdays
	}

	// Today classification (weekend / holiday / next business day).
	// Computed last so it sees the same "now" the rest of the
	// dashboard uses; the safety-cap'd forward walker handles the
	// edge case where the holiday config is broken.
	h.loadTodayContext(now, data)

	// The chairs row is conditional on the cap being set; when
	// the picker is a no-op (cap <= 0 or service unconfigured) the
	// loader leaves the data map untouched and the template
	// suppresses the row.
	h.loadChairsData(ctx, data, today)
}

// loadCurrentHAT populates the HAT banner data — who is actually on
// support today, with the primary/cover split and the "on leave"
// flag so the template can render the status note.
func (h *Handler) loadCurrentHAT(ctx context.Context, data map[string]any) {
	today := time.Now().Format("2006-01-02")

	assignments, err := h.db.GetAssignmentsByDate(ctx, today)
	if err != nil {
		return
	}

	primary, cover := splitPrimaryAndCover(assignments)
	if primary == nil {
		return
	}

	onLeave := h.isPrimaryOnLeaveToday(ctx, primary.MemberID, today)
	onCallMemberID := primary.MemberID
	if onLeave && cover != nil {
		onCallMemberID = cover.MemberID
	}

	onCallMember, err := h.db.GetMemberByID(ctx, onCallMemberID)
	if err != nil || onCallMember == nil {
		return
	}

	primaryMember, _ := h.db.GetMemberByID(ctx, primary.MemberID)

	data["CurrentHATName"] = onCallMember.Name
	data["CurrentHATIsOnLeave"] = onLeave
	if primaryMember != nil {
		data["CurrentHATPrimaryName"] = primaryMember.Name
	}
}

// splitPrimaryAndCover separates today's assignments into the
// primary (IsCover=false) and the cover (IsCover=true). The schedule
// engine guarantees at most one of each for any given day.
func splitPrimaryAndCover(assignments []database.RotaAssignment) (primary, cover *database.RotaAssignment) {
	for i := range assignments {
		if assignments[i].IsCover {
			cover = &assignments[i]
		} else {
			primary = &assignments[i]
		}
	}
	return primary, cover
}

// isPrimaryOnLeaveToday reports whether the named member has an
// active leave record (status pending or assigned) covering today.
func (h *Handler) isPrimaryOnLeaveToday(ctx context.Context, memberID, today string) bool {
	leaves, err := h.db.GetLeaveRecords(ctx)
	if err != nil {
		return false
	}
	for i := range leaves {
		if leaves[i].MemberID != memberID {
			continue
		}
		if !rota.IsLeaveActive(leaves[i].Status) {
			continue
		}
		start := leaves[i].StartDate.Format("2006-01-02")
		end := leaves[i].EndDate.Format("2006-01-02")
		if start <= today && today <= end {
			return true
		}
	}
	return false
}

// loadMeetingsToken surfaces the user's first calendar subscription
// token so the schedule matrix can link each date header to the
// per-day meetings page. nil token means no subscription — links
// fall back to the dashboard.
func (h *Handler) loadMeetingsToken(ctx context.Context, data map[string]any) {
	user, ok := auth.GetUserFromContext(ctx)
	if !ok {
		return
	}
	member, mErr := h.db.GetMemberByEmail(ctx, user.Email)
	if mErr != nil || member == nil {
		return
	}
	subs, sErr := h.db.GetSubscriptionsByMemberID(ctx, member.ID)
	if sErr != nil || len(subs) == 0 {
		return
	}
	data["MeetingsToken"] = subs[0].Token
}

// wfhFloor returns the WFH min-onsite count for the team, or 0 if
// the WFH service is not configured. The schedule matrix uses it
// to flag columns at the WFH floor.
func (h *Handler) wfhFloor(ctx context.Context) int {
	if h.wfhService == nil {
		return 0
	}
	members, err := h.db.GetActiveTeamMembers(ctx)
	if err != nil {
		return 0
	}
	return h.wfhService.MinOnsiteCount(len(members))
}

// loadChairsData populates today's "on-site headcount vs. seat cap"
// trio. Source of truth: the schedule matrix's presence snapshot —
// it already accounts for assignments, leave, WFH, covers, and
// exemptions, so reusing it keeps the chairs row from drifting.
// Falls back to a recompute path when the snapshot is empty (test
// fixtures with no business-day setup).
func (h *Handler) loadChairsData(ctx context.Context, data map[string]any, today string) {
	seatCap, ok := h.chairsSeatCap()
	if !ok {
		return
	}

	if onSite, snapshotOK := onSiteFromPresenceSnapshot(data); snapshotOK {
		h.writeChairsFields(data, onSite, seatCap)
		return
	}

	members, err := h.db.GetActiveTeamMembers(ctx)
	if err != nil || len(members) == 0 {
		return
	}

	onLeave, _ := h.countActiveLeaveToday(ctx, today)
	wfhToday, _ := h.countApprovedWFHToday(ctx, today)

	onSite := max(0, len(members)-onLeave-wfhToday)
	h.writeChairsFields(data, onSite, seatCap)
}

// onSiteFromPresenceSnapshot returns today's on-site headcount from
// the schedule matrix's presence snapshot.
func onSiteFromPresenceSnapshot(data map[string]any) (int, bool) {
	rawPresence, ok := data["UpcomingPresence"].([]presenceDay)
	if !ok || len(rawPresence) == 0 {
		return 0, false
	}
	return len(rawPresence[0].Present), true
}

// writeChairsFields sets the four template fields and applies the
// percent clamp + color-band mapping.
func (h *Handler) writeChairsFields(data map[string]any, onSite, seatCap int) {
	percent := clampPercent((onSite * chairsPercentMultiplier) / seatCap)
	color := chairsColorForPercent(percent)

	data["ChairsOnSite"] = onSite
	data["ChairsTotal"] = seatCap
	data["ChairsPercent"] = percent
	data["ChairsColor"] = color
}

// chairsPercentMultiplier and chairsPercentMax are the inline
// constants for the ratio's percentage band.
const (
	chairsPercentMultiplier = 100
	chairsPercentMax        = 999
)

// chairsSeatCap returns the configured seat cap, or (0, false) when
// the cap is unset or the service is missing.
func (h *Handler) chairsSeatCap() (int, bool) {
	if h.wfhService == nil {
		return 0, false
	}
	seatCap := h.wfhService.Config().SeatCap
	if seatCap <= 0 {
		return 0, false
	}
	return seatCap, true
}

// countActiveLeaveToday counts distinct leave records that should
// subtract from today's on-site headcount.
func (h *Handler) countActiveLeaveToday(ctx context.Context, today string) (int, error) {
	leaveRows, err := h.db.GetLeaveByDate(ctx, today)
	if err != nil {
		return 0, err
	}
	count := 0
	for i := range leaveRows {
		status := strings.ToLower(strings.TrimSpace(leaveRows[i].Status))
		if status == "pending" || status == "assigned" {
			count++
		}
	}
	return count, nil
}

// countApprovedWFHToday counts approved WFH rows for today. Pending
// rows haven't settled yet and withdrawn rows are stale.
func (h *Handler) countApprovedWFHToday(ctx context.Context, today string) (int, error) {
	wfhRows, err := h.db.GetWFHRequestsByDate(ctx, today)
	if err != nil {
		return 0, err
	}
	count := 0
	for i := range wfhRows {
		if wfhRows[i].Status == database.WFHStatusApproved {
			count++
		}
	}
	return count, nil
}

// clampPercent caps the ratio's percentage display at
// chairsPercentMax so an absurd over-cap state doesn't render a
// four-digit value.
func clampPercent(percent int) int {
	if percent > chairsPercentMax {
		return chairsPercentMax
	}
	return percent
}

// chairsColorForPercent maps the ratio to a Bulma tag class.
func chairsColorForPercent(percent int) string {
	switch {
	case percent > chairsPercentMultiplier:
		return "is-danger"
	case percent == chairsPercentMultiplier:
		return "is-warning"
	default:
		return "is-success"
	}
}

// isConferenceLeaveToday reports whether the member has an active
// leave row for today whose leave_type is "conference".
func isConferenceLeaveToday(ctx context.Context, db *database.DB, memberID, today string) bool {
	leaves, err := db.GetLeaveByDate(ctx, today)
	if err != nil {
		return false
	}
	for i := range leaves {
		if leaves[i].MemberID != memberID {
			continue
		}
		return leaves[i].LeaveType == database.LeaveTypeConference
	}
	return false
}

// getAssignedSwapInfo builds the per-day cell tooltip for swap
// assignments. Returns "" when there is no swap, an inline "Accepted
// swap assignment." when the enriched swap is missing names, and a
// fully formatted "Accepted swap: A (dateX) ↔ B (dateY)" otherwise.
//
//nolint:cyclop // Swap tooltip enrichment has guard clauses for several fallback states.
func (h *Handler) getAssignedSwapInfo(ctx context.Context, assignmentID string) (swapInfo string) {
	if assignmentID == "" {
		return ""
	}

	swap, err := h.db.GetAcceptedSwapForAssignment(ctx, assignmentID)
	if err != nil {
		if errors.Is(err, database.ErrSwapNotFound) {
			return ""
		}
		return ""
	}
	if swap == nil {
		return ""
	}

	enrichedSwaps, err := h.db.GetEnrichedSwaps(ctx, []database.HatSwap{*swap})
	if err != nil || len(enrichedSwaps) == 0 {
		return ""
	}
	s := enrichedSwaps[0]
	if s.RequesterName == "" || s.TargetName == "" || s.RequesterDate == "" || s.TargetDate == "" {
		return "Accepted swap assignment."
	}
	return "Accepted swap: " + s.RequesterName + " (" + s.RequesterDate + ") ↔ " + s.TargetName + " (" + s.TargetDate + ")"
}
