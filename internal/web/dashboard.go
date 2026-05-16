package web

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
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
// It returns the member, whether the assignment was swapped, and whether any assignment was found.
func (h *Handler) getAssignedMember(ctx context.Context, dateStr string, memberMap map[string]database.TeamMember) (*database.TeamMember, bool) {
	assignments, err := h.db.GetAssignmentsByDate(ctx, dateStr)
	if err != nil {
		return nil, false
	}

	// Prioritize cover assignment - they're the one actually doing support.
	for i := range assignments {
		if assignments[i].IsCover {
			if member, ok := memberMap[assignments[i].MemberID]; ok {
				return &member, assignments[i].IsSwapped
			}
		}
	}

	// Fall back to original assignment if no cover.
	for i := range assignments {
		if !assignments[i].IsCover {
			if member, ok := memberMap[assignments[i].MemberID]; ok {
				return &member, assignments[i].IsSwapped
			}
		}
	}

	return nil, false
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
		assigned, assignedSwapped := h.getAssignedMember(ctx, dateStr, memberMap)

		leaveRecords, leaveErr := h.db.GetLeaveByDate(ctx, dateStr)
		if leaveErr != nil {
			return nil, leaveErr
		}

		away := make([]presenceLeave, 0, len(leaveRecords))
		onLeave := make(map[string]struct{})
		for i := range leaveRecords {
			leave := &leaveRecords[i]
			member, ok := memberMap[leave.MemberID]
			if !ok {
				continue
			}
			onLeave[leave.MemberID] = struct{}{}
			away = append(away, presenceLeave{Member: member})
		}

		present := buildPresenceList(memberMap, onLeave)

		sort.Slice(away, func(i, j int) bool {
			return away[i].Member.Name < away[j].Member.Name
		})

		now := time.Now()
		isToday := current.Year() == now.Year() && current.YearDay() == now.YearDay()

		presence = append(presence, presenceDay{
			DateISO:         dateStr,
			DateDisplay:     current.Format("Mon, Jan 2"),
			IsToday:         isToday,
			Assigned:        assigned,
			AssignedSwapped: assignedSwapped,
			Present:         present,
			Away:            away,
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
