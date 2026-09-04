package web

import (
	"context"
	"sort"
	"time"

	"github.com/inful/madhatter/internal/database"
)

// buildScheduleMatrix assembles the matrix view-model from a slice
// of presenceDay snapshots. The function is data-oriented and
// intentionally explicit — every cell condition is named, not
// factored into smaller helpers, because the matrix is the
// canonical "who is where today" surface and reviewers need to see
// the entire decision tree at once.
//
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
				cell.LeaveType = awayLeaveType(day, member.ID)
				// Differentiate conference leaves on the cell label so
				// the matrix reads "Conference" instead of a generic
				// "Away" when the row is a tagged conference day.
				if cell.LeaveType == database.LeaveTypeConference {
					cell.Label = "Conference"
				} else {
					cell.Label = "Away"
				}
			case isWFHMember(day, member.ID):
				cell.Status = "wfh"
				cell.Label = "WFH"
				// Flag admin-marked WFH so the template renders the
				// chip in the distinct purple-blue color. The flag
				// travels with the cell; the presenceDay owns the
				// authoritative set.
				_, cell.IsAdminMarkedWFH = day.AdminMarkedMemberIDs[member.ID]
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

// awayLeaveType reports the leave_type for the absent member in the
// day, or empty when the member isn't away.
func awayLeaveType(day presenceDay, memberID string) string {
	for i := range day.Away {
		if day.Away[i].Member.ID == memberID {
			return day.Away[i].LeaveType
		}
	}
	return ""
}

// getUpcomingHolidays returns upcoming holidays for the configured
// lookahead days.
func (h *Handler) getUpcomingHolidays() []map[string]any {
	var holidays []map[string]any
	now := time.Now()
	endDate := now.AddDate(0, 0, defaultHolidayLookaheadDays)

	for d := now; d.Before(endDate); d = d.AddDate(0, 0, 1) {
		// Skip weekends - they are not holidays.
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}

		if h.holidayChecker(d) {
			holidays = append(holidays, map[string]any{
				"Date": d.Format("2006-01-02"),
				"Name": "Holiday",
			})
		}
	}

	return holidays
}

// isBusinessDay reports whether the date is a weekday AND not a
// holiday. Used by both the orchestrator (gating the WFH button)
// and the matrix builder (skipping weekends).
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

// getAssignedMember fetches the assigned member and swap status for
// a given date. It returns the member, whether the assignment was
// swapped, and the selected assignment ID.
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

// buildPresenceList creates a sorted list of present members
// (everyone minus those on leave).
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

// getUpcomingPresenceFrom assembles the per-day presence slices for
// the next weekDaysCount business days starting at start. Combines
// rota assignments, leave, and WFH (approved-explicit + recurring-
// contractual) into a unified presenceDay view per date.
//
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
		// AdminMarkedMemberIDs is the subset of WFH members whose
		// row was inserted by an admin via /admin/wfh/mark. The
		// matrix renders those members' WFH cells in the distinct
		// purple-blue color, separate from the user-requested blue.
		adminMarkedMemberIDs := make(map[string]struct{})
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
			// Default LeaveType to plain leave so the template's switch
			// doesn't have to special-case an empty value (older rows
			// pre-migration, or anything that ever bypassed validation).
			leaveType := leave.LeaveType
			if leaveType == "" {
				leaveType = database.LeaveTypeLeave
			}
			away = append(away, presenceLeave{Member: member, LeaveType: leaveType})
		}

		for i := range wfhRequests {
			if wfhRequests[i].Status == database.WFHStatusApproved {
				wfhMemberIDs[wfhRequests[i].MemberID] = struct{}{}
				if wfhRequests[i].IsAdminMarked {
					adminMarkedMemberIDs[wfhRequests[i].MemberID] = struct{}{}
				}
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
			DateISO:              dateStr,
			DateDisplay:          current.Format("Mon, Jan 2"),
			IsToday:              isToday,
			Assigned:             assigned,
			AssignedSwapped:      assignedSwapped,
			AssignedSwapInfo:     assignedSwapInfo,
			Present:              onsite,
			WFH:                  wfh,
			Away:                 away,
			AdminMarkedMemberIDs: adminMarkedMemberIDs,
		})

		current = current.AddDate(0, 0, 1)
	}

	return presence, nil
}

// buildWeekData builds week data for the dashboard display.
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

	startDate := currentWeekStart.Format("2006-01-02")
	endDate := nextWeekEnd.Format("2006-01-02")

	assignments, err := h.db.GetAssignmentsByDateRange(ctx, startDate, endDate)
	if err != nil {
		return nil, err
	}

	result := make(map[string][]database.RotaAssignment)
	for _, a := range assignments {
		result[a.Date] = append(result[a.Date], a)
	}

	return result, nil
}
