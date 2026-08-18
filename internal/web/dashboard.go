package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/rota"
)

const (
	currentUserStatusOnLeave = "On leave"
	currentUserStatusWFH     = "WFH"
	currentUserStatusOnSite  = "On-site"
)

func (h *Handler) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := map[string]any{
		"Today":    time.Now().Format("Monday, Jan 2, 2006"),
		"Template": "dashboard",
	}

	data["AuthEnabled"] = h.authManager != nil && h.authMiddleware != nil

	// Add user info to data.
	if user, ok := auth.GetUserFromContext(ctx); ok {
		data["User"] = user
		data["IsAdmin"] = auth.IsAdminSession(user)
		h.loadCurrentUserPresenceStatus(ctx, data, user.Email)
	}

	// Check team members - show message if none exist, but always load schedule.
	members, err := h.db.GetActiveTeamMembers(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if len(members) == 0 {
		data["NoTeamMessage"] = "No team members found. Please add team members to get started."
	}

	// Always maintain and load schedule, even if no team members.
	_, err = h.maintenance.EnsureSchedule(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Load the HAT banner (today's HAT + cover status) after the
	// schedule is ensured — the banner needs the assignment row to
	// exist so the answer is consistent with the matrix below.
	h.loadCurrentHAT(ctx, data)

	// Load dashboard data.
	h.loadDashboardData(ctx, data)

	// Load pending swap count for logged-in user.
	h.loadPendingSwapCount(ctx, data)

	// Render template.
	if err := h.tmpl.ExecuteTemplate(w, "dashboard.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) loadPendingSwapCount(ctx context.Context, data map[string]any) {
	user, ok := auth.GetUserFromContext(ctx)
	if !ok {
		return
	}

	member, err := h.db.GetMemberByEmail(ctx, user.Email)
	if err != nil || member == nil {
		return
	}

	count, err := h.db.CountPendingSwapsForMember(ctx, member.ID)
	if err == nil && count > 0 {
		data["PendingSwapCount"] = count
	}
}

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

	h.loadCurrentUserUpcomingDates(ctx, data, member.ID, today, leaveDates)

	if _, onLeave := leaveDates[today]; onLeave {
		data["CurrentUserPresenceStatus"] = currentUserStatusOnLeave
		return
	}

	// WFH status: look up today's row for this member across all
	// statuses (not just approved). The DB is the source of truth — a
	// withdrawn, denied, cancelled, or pending row means the member
	// is NOT actually WFH today, regardless of their contractual
	// recurring weekday. Falling back to IsRecurringWFHOn before the
	// DB lookup was the bug: a self-withdrawn recurring WFH would
	// silently surface as "WFH" on the dashboard until the next
	// materialiser tick noticed the withdrawal.
	allWFHRequests, wfhErr := h.db.GetWFHRequestsByDate(ctx, today)
	if wfhErr == nil {
		for i := range allWFHRequests {
			if allWFHRequests[i].MemberID != member.ID {
				continue
			}
			if allWFHRequests[i].Status == database.WFHStatusApproved {
				data["CurrentUserPresenceStatus"] = currentUserStatusWFH
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
// given member is on an *active* leave. Only leaves with status
// "pending" or "assigned" are included - the same set the rota engine
// treats as live (see rota.isLeaveActive). Rejected, cancelled, and
// completed leaves are ignored, so a rejected leave with a future date
// does not gate the HAT-day surface.
//
// Leaves that have already ended are excluded, and leaves that started
// in the past are clamped to today.
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

//nolint:cyclop // Upcoming date resolution combines multiple domain queries.
func (h *Handler) loadCurrentUserUpcomingDates(ctx context.Context, data map[string]any, memberID, today string, leaveDates map[string]struct{}) {
	// Next HAT: the earliest future day the user is on HAT duty. Cover
	// duties count - the user is on support that day, just for someone
	// else. Days the user is on leave are skipped because in that case a
	// cover has the duty, not the user.
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

	// Next leave day: reuses the pre-computed leave-date set so we only
	// surface dates that are still in the future.
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

func (h *Handler) handleLoginUnavailable(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("Authentication is disabled on this server.\n\nTo enable login, configure OAuth provider environment variables (see AUTH_SETUP.md), or run the server with --development for fake login during local development.\n"))
}

// loadDashboardData populates the dashboard with today's and week's assignments.
func (h *Handler) loadDashboardData(ctx context.Context, data map[string]any) {
	// Get today's assignment.
	today := time.Now().Format("2006-01-02")
	assignments, err := h.db.GetAssignmentsByDate(ctx, today)
	if err == nil && len(assignments) > 0 {
		data["TodayAssignment"] = assignments[0]
	}

	// Get upcoming presence for next business days.
	if presence, presenceErr := h.getUpcomingPresence(ctx); presenceErr == nil {
		data["UpcomingPresence"] = presence
		data["ScheduleMatrix"] = buildScheduleMatrix(presence, h.wfhFloor(ctx))
	}

	// Get current and next week assignments.
	weeksData, err := h.getFullWeeks(ctx)
	if err == nil {
		data["CurrentWeek"] = h.buildWeekData(weeksData, true)
		data["NextWeek"] = h.buildWeekData(weeksData, false)
	}

	// Get upcoming holidays.
	if h.holidayChecker != nil {
		data["UpcomingHolidays"] = h.getUpcomingHolidays()
	}

	h.loadMeetingsToken(ctx, data)
}

// loadCurrentHAT populates the HAT banner data — the most-asked
// question of a rota app: who is on support today. Sets:
//
//   - CurrentHATName: name of the person actually on call today
//     (the cover if the primary is on leave, otherwise the primary).
//   - CurrentHATIsOnLeave: true when the primary HAT is on leave
//     today (regardless of whether a cover was assigned).
//   - CurrentHATPrimaryName: name of the primary HAT, used in the
//     "X on leave" status note when the primary is on leave.
//
// The banner is rendered between the status card and the schedule
// card on the dashboard. If no primary assignment exists for today
// (the schedule maintenance guarantees a 14-day window, so this
// should be rare — first day of operation, before maintenance runs,
// or a fixture gap), no data fields are set and the template
// suppresses the banner.
//
// Cover assignment is the engine's existing mechanism: when the
// primary HAT is on leave, the engine reassigns the day to a cover
// and the cover is the person actually on call. The on-call name
// reported by the banner is the cover in that case; the primary's
// name is reported alongside the "on leave" status so the user gets
// the full story in one glance.
func (h *Handler) loadCurrentHAT(ctx context.Context, data map[string]any) {
	today := time.Now().Format("2006-01-02")

	assignments, err := h.db.GetAssignmentsByDate(ctx, today)
	if err != nil {
		return
	}

	primary, cover := splitPrimaryAndCover(assignments)
	if primary == nil {
		return // No HAT today — template suppresses the banner.
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
// primary (IsCover=false) and the cover (IsCover=true). The
// schedule engine guarantees at most one of each for any given day,
// so the result is at most one primary and at most one cover.
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
// active leave record (status pending or assigned per the engine's
// definition) that covers today. The status filter is shared with
// the engine's reassignment logic so the dashboard's "is on leave"
// answer stays consistent with the engine's actual cover path.
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
// per-day meetings page. nil token means no subscription, so the
// links fall back to the dashboard.
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

// wfhFloor returns the WFH min-onsite count for the team, or 0
// if the WFH service is not configured. The schedule matrix uses
// it to flag columns that have hit the WFH floor with an orange
// tone on the WFH icon.
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

//nolint:cyclop // Matrix assembly is data-oriented and intentionally explicit.
func buildScheduleMatrix(presence []presenceDay, floor int) scheduleMatrix {
	memberByID := make(map[string]database.TeamMember)

	for i := range presence {
		day := presence[i]
		for i := range day.Present {
			memberByID[day.Present[i].ID] = day.Present[i]
		}
		for i := range day.WFH {
			memberByID[day.WFH[i].ID] = day.WFH[i]
		}
		for i := range day.Away {
			memberByID[day.Away[i].Member.ID] = day.Away[i].Member
		}
	}

	members := make([]database.TeamMember, 0, len(memberByID))
	for _, member := range memberByID {
		members = append(members, member)
	}
	sort.Slice(members, func(i, j int) bool {
		return members[i].Name < members[j].Name
	})

	days := make([]scheduleMatrixDay, 0, len(presence))
	for i := range presence {
		day := presence[i]
		atWork := len(day.Present)
		days = append(days, scheduleMatrixDay{
			DateISO:     day.DateISO,
			DateDisplay: day.DateDisplay,
			IsToday:     day.IsToday,
			AtWorkCount: atWork,
			WFHCount:    len(day.WFH),
			LeaveCount:  len(day.Away),
			// Flag the column when the at-work count has reached
			// the WFH min-onsite floor. The "<=" covers both the
			// "at the floor" case (no more WFH allowed) and the
			// over-WFH case (which shouldn't happen but is a
			// strong signal worth flagging).
			AtWFHFloor: floor > 0 && atWork <= floor,
		})
	}

	rows := make([]scheduleMatrixRow, 0, len(members))
	for _, member := range members {
		row := scheduleMatrixRow{Member: member, Cells: make([]scheduleMatrixCell, 0, len(presence))}
		for i := range presence {
			day := presence[i]
			cell := scheduleMatrixCell{
				Status:    "none",
				Label:     "-",
				IsToday:   day.IsToday,
				DateISO:   day.DateISO,
				DateLabel: day.DateDisplay,
			}

			switch {
			case isAwayMember(day, member.ID):
				cell.Status = "away"
				cell.Label = "Away"
			case isWFHMember(day, member.ID):
				cell.Status = "wfh"
				cell.Label = "WFH"
			case isPresentMember(day, member.ID):
				cell.Status = "onsite"
				cell.Label = "On-site"
			}

			if day.Assigned != nil && day.Assigned.ID == member.ID {
				cell.Assigned = true
				cell.Swapped = day.AssignedSwapped
				cell.SwapInfo = day.AssignedSwapInfo
			}

			row.Cells = append(row.Cells, cell)
		}
		rows = append(rows, row)
	}

	return scheduleMatrix{Days: days, Rows: rows}
}

func isPresentMember(day presenceDay, memberID string) bool {
	for i := range day.Present {
		if day.Present[i].ID == memberID {
			return true
		}
	}
	return false
}

func isWFHMember(day presenceDay, memberID string) bool {
	for i := range day.WFH {
		if day.WFH[i].ID == memberID {
			return true
		}
	}
	return false
}

func isAwayMember(day presenceDay, memberID string) bool {
	for i := range day.Away {
		if day.Away[i].Member.ID == memberID {
			return true
		}
	}
	return false
}

// getUpcomingHolidays returns upcoming holidays for the configured lookahead days.
func (h *Handler) getUpcomingHolidays() []map[string]any {
	var holidays []map[string]any
	now := time.Now()
	endDate := now.AddDate(0, 0, defaultHolidayLookaheadDays)

	for d := now; d.Before(endDate); d = d.AddDate(0, 0, 1) {
		// Skip weekends - they are not holidays.
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}

		// Check if it's an actual holiday (not just a skipped date).
		if h.holidayChecker(d) {
			holidays = append(holidays, map[string]any{
				"Date": d.Format("2006-01-02"),
				"Name": "Holiday",
			})
		}
	}

	return holidays
}

func (h *Handler) isBusinessDay(date time.Time) bool {
	if date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
		return false
	}

	if h.holidayChecker != nil && h.holidayChecker(date) {
		return false
	}

	return true
}

func (h *Handler) getUpcomingPresence(ctx context.Context) ([]presenceDay, error) {
	return h.getUpcomingPresenceFrom(ctx, time.Now())
}

// getAssignedMember fetches the assigned member and swap status for a given date.
// It returns the member, whether the assignment was swapped, and the selected assignment ID.
func (h *Handler) getAssignedMember(ctx context.Context, dateStr string, memberMap map[string]database.TeamMember) (*database.TeamMember, bool, string) {
	assignments, err := h.db.GetAssignmentsByDate(ctx, dateStr)
	if err != nil {
		return nil, false, ""
	}

	// Prioritize cover assignment - they're the one actually doing support.
	for i := range assignments {
		if assignments[i].IsCover {
			if member, ok := memberMap[assignments[i].MemberID]; ok {
				return &member, assignments[i].IsSwapped, assignments[i].ID
			}
		}
	}

	// Fall back to original assignment if no cover.
	for i := range assignments {
		if !assignments[i].IsCover {
			if member, ok := memberMap[assignments[i].MemberID]; ok {
				return &member, assignments[i].IsSwapped, assignments[i].ID
			}
		}
	}

	return nil, false, ""
}

//nolint:cyclop // Swap tooltip enrichment has guard clauses for several fallback states.
func (h *Handler) getAssignedSwapInfo(ctx context.Context, assignmentID string) string {
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

	return fmt.Sprintf("Accepted swap: %s (%s) ↔ %s (%s)", s.RequesterName, s.RequesterDate, s.TargetName, s.TargetDate)
}

// buildPresenceList creates a sorted list of present members.
func buildPresenceList(memberMap map[string]database.TeamMember, onLeave map[string]struct{}) []database.TeamMember {
	present := make([]database.TeamMember, 0, len(memberMap)-len(onLeave))

	for id, member := range memberMap {
		if _, absent := onLeave[id]; absent {
			continue
		}
		present = append(present, member)
	}

	sort.Slice(present, func(i, j int) bool {
		return present[i].Name < present[j].Name
	})

	return present
}

//nolint:cyclop // Presence assembly coordinates assignment, leave, and WFH sources.
func (h *Handler) getUpcomingPresenceFrom(ctx context.Context, start time.Time) ([]presenceDay, error) {
	members, err := h.db.GetActiveTeamMembers(ctx)
	if err != nil {
		return nil, err
	}

	memberMap := make(map[string]database.TeamMember, len(members))
	for _, member := range members {
		memberMap[member.ID] = member
	}

	current := start
	presence := make([]presenceDay, 0, weekDaysCount)

	for len(presence) < weekDaysCount {
		if !h.isBusinessDay(current) {
			current = current.AddDate(0, 0, 1)
			continue
		}

		dateStr := current.Format("2006-01-02")
		assigned, assignedSwapped, assignedID := h.getAssignedMember(ctx, dateStr, memberMap)
		assignedSwapInfo := ""
		if assignedSwapped {
			assignedSwapInfo = h.getAssignedSwapInfo(ctx, assignedID)
		}

		leaveRecords, leaveErr := h.db.GetLeaveByDate(ctx, dateStr)
		if leaveErr != nil {
			return nil, leaveErr
		}

		wfhRequests, wfhErr := h.db.GetWFHRequestsByDate(ctx, dateStr)
		if wfhErr != nil {
			return nil, wfhErr
		}

		away := make([]presenceLeave, 0, len(leaveRecords))
		onLeave := make(map[string]struct{})
		wfhMemberIDs := make(map[string]struct{}, len(wfhRequests))
		// explicitNonWFHSet tracks members whose wfh_requests row for
		// this date is in a non-approved status (withdrawn, denied,
		// cancelled, or pending). The IsRecurringWFHOn fallback below
		// must NOT add these members to wfhMemberIDs — an explicit
		// withdrawal or denial must take precedence over the contractual
		// recurring weekday, otherwise self-withdraw is silently undone
		// on the dashboard until the next settlement tick re-materializes
		// the row.
		explicitNonWFHSet := make(map[string]struct{}, len(wfhRequests))
		for i := range leaveRecords {
			leave := &leaveRecords[i]
			member, ok := memberMap[leave.MemberID]
			if !ok {
				continue
			}
			onLeave[leave.MemberID] = struct{}{}
			away = append(away, presenceLeave{Member: member})
		}

		for i := range wfhRequests {
			if wfhRequests[i].Status == database.WFHStatusApproved {
				wfhMemberIDs[wfhRequests[i].MemberID] = struct{}{}
			} else {
				explicitNonWFHSet[wfhRequests[i].MemberID] = struct{}{}
			}
		}

		for i := range members {
			if !members[i].IsRecurringWFHOn(current) {
				continue
			}
			// Honor any explicit non-approved decision for this date.
			// Without this guard a self-withdrawn recurring WFH still
			// surfaces as WFH on the dashboard until the next materializer
			// run sees the withdrawn row, leaving the user confused for
			// an unbounded window.
			if _, explicitNonWFH := explicitNonWFHSet[members[i].ID]; explicitNonWFH {
				continue
			}
			wfhMemberIDs[members[i].ID] = struct{}{}
		}

		present := buildPresenceList(memberMap, onLeave)
		onsite := make([]database.TeamMember, 0, len(present))
		wfh := make([]database.TeamMember, 0, len(wfhMemberIDs))
		for i := range present {
			if _, ok := wfhMemberIDs[present[i].ID]; ok {
				wfh = append(wfh, present[i])
				continue
			}
			onsite = append(onsite, present[i])
		}

		sort.Slice(away, func(i, j int) bool {
			return away[i].Member.Name < away[j].Member.Name
		})

		now := time.Now()
		isToday := current.Year() == now.Year() && current.YearDay() == now.YearDay()

		presence = append(presence, presenceDay{
			DateISO:          dateStr,
			DateDisplay:      current.Format("Mon, Jan 2"),
			IsToday:          isToday,
			Assigned:         assigned,
			AssignedSwapped:  assignedSwapped,
			AssignedSwapInfo: assignedSwapInfo,
			Present:          onsite,
			WFH:              wfh,
			Away:             away,
		})

		current = current.AddDate(0, 0, 1)
	}

	return presence, nil
}

// buildWeekData builds week data for dashboard display.
func (h *Handler) buildWeekData(weeksData map[string][]database.RotaAssignment, isCurrentWeek bool) []map[string]any {
	now := time.Now()
	weekStart := now.AddDate(0, 0, -int(now.Weekday())+1)
	if !isCurrentWeek {
		weekStart = weekStart.AddDate(0, 0, weekDaysInWeek)
	}
	weekEnd := weekStart.AddDate(0, 0, weekDaysOffset)

	var week []map[string]any
	for d := weekStart; d.Before(weekEnd.AddDate(0, 0, 1)); d = d.AddDate(0, 0, 1) {
		dateStr := d.Format("2006-01-02")
		isHoliday := h.holidayChecker != nil && h.holidayChecker(d)

		var assignment database.RotaAssignment
		if assignments, ok := weeksData[dateStr]; ok && len(assignments) > 0 {
			assignment = assignments[0]
		} else {
			assignment = database.RotaAssignment{
				Date:       dateStr,
				MemberName: "Unassigned",
			}
		}

		week = append(week, map[string]any{
			"Assignment": assignment,
			"IsHoliday":  isHoliday,
		})
	}
	return week
}

// getFullWeeks retrieves assignments for current and next week.
func (h *Handler) getFullWeeks(ctx context.Context) (map[string][]database.RotaAssignment, error) {
	now := time.Now()

	// Current week (Monday to Friday).
	currentWeekStart := now.AddDate(0, 0, -int(now.Weekday())+1)

	// Next week (Monday to Friday).
	nextWeekStart := currentWeekStart.AddDate(0, 0, weekDaysInWeek)
	nextWeekEnd := nextWeekStart.AddDate(0, 0, weekDaysOffset)

	// Get all assignments for both weeks.
	startDate := currentWeekStart.Format("2006-01-02")
	endDate := nextWeekEnd.Format("2006-01-02")

	assignments, err := h.db.GetAssignmentsByDateRange(ctx, startDate, endDate)
	if err != nil {
		return nil, err
	}

	// Build map by date.
	result := make(map[string][]database.RotaAssignment)
	for _, a := range assignments {
		result[a.Date] = append(result[a.Date], a)
	}

	return result, nil
}
