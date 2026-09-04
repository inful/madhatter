package database

import "time"

// RecurringWFHDays is the bitmask-shaped view of the five
// recurring-WFH flags on team_members. Used by SetTeamMemberRecurringWFHDays
// as the input shape and by the recurring-WFH materializer as the
// schedule template.
type RecurringWFHDays struct {
	Monday    bool
	Tuesday   bool
	Wednesday bool
	Thursday  bool
	Friday    bool
}

// TeamMember is the application-level team member shape. The
// sqlc-generated TeamMember (internal/database/sqlc) is translated
// into this shape by teamMemberFromSQLC; the conversion unpacks the
// nullable int64 flags into bools and exposes the contractual-day
// helpers below.
type TeamMember struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	Email                 string `json:"email"`
	IsActive              bool   `json:"is_active"`
	RecurringWFHMonday    bool   `json:"recurring_wfh_monday"`
	RecurringWFHTuesday   bool   `json:"recurring_wfh_tuesday"`
	RecurringWFHWednesday bool   `json:"recurring_wfh_wednesday"`
	RecurringWFHThursday  bool   `json:"recurring_wfh_thursday"`
	RecurringWFHFriday    bool   `json:"recurring_wfh_friday"`
	// Deprecated semantic alias; true when all weekdays are recurring WFH.
	IsPermanentWFH bool `json:"is_permanent_wfh"`
	// IsExemptFromAssignment is true when an admin has marked the
	// member as exempt from the seat-cap picker. An exempt member
	// is never selected as an involuntary Assigned WFH candidate,
	// but their voluntary WFHs still count against the on-site
	// capacity math and they can still volunteer via a swap.
	// Separate concept from IsPermanentWFH (a permanent on-site
	// exception; the picker also excludes permanent-WFH members
	// from its candidate pool).
	IsExemptFromAssignment bool      `json:"is_exempt_from_assignment"`
	CreatedAt              time.Time `json:"created_at"`
}

// IsRecurringWFHOn reports whether the member has a contractual
// recurring WFH day on date.
func (m TeamMember) IsRecurringWFHOn(date time.Time) bool {
	switch date.Weekday() {
	case time.Monday:
		return m.RecurringWFHMonday
	case time.Tuesday:
		return m.RecurringWFHTuesday
	case time.Wednesday:
		return m.RecurringWFHWednesday
	case time.Thursday:
		return m.RecurringWFHThursday
	case time.Friday:
		return m.RecurringWFHFriday
	case time.Saturday:
		return false
	case time.Sunday:
		return false
	default:
		return false
	}
}

// HasPermanentRecurringWFH reports whether all weekdays are
// configured as recurring WFH.
func (m TeamMember) HasPermanentRecurringWFH() bool {
	return m.RecurringWFHMonday &&
		m.RecurringWFHTuesday &&
		m.RecurringWFHWednesday &&
		m.RecurringWFHThursday &&
		m.RecurringWFHFriday
}

// HasAnyRecurringWFH reports whether the member has at least one
// recurring WFH weekday configured. Used by the materializer as a
// fast-path skip.
func (m TeamMember) HasAnyRecurringWFH() bool {
	return m.RecurringWFHMonday ||
		m.RecurringWFHTuesday ||
		m.RecurringWFHWednesday ||
		m.RecurringWFHThursday ||
		m.RecurringWFHFriday
}

// LeaveTypeLeave and LeaveTypeConference are the closed set of
// values leave_records.leave_type accepts. The DB enforces the
// same set via a CHECK constraint (migration 000021); keeping the
// constants here lets the application reject bad values before
// they reach the driver and gives call sites a typed comparison
// instead of raw string equality.
const (
	LeaveTypeLeave      = "leave"
	LeaveTypeConference = "conference"
)

// IsValidLeaveType reports whether v is one of the accepted leave
// types. Callers should use this when accepting leave_type from
// user input so the application rejects the same set the DB CHECK
// constraint would.
func IsValidLeaveType(v string) bool {
	switch v {
	case LeaveTypeLeave, LeaveTypeConference:
		return true
	default:
		return false
	}
}

// LeaveRecord is the application-level leave shape. Translates
// the sqlc leave_records.sql.go row via LeaveRecordFromSQLC.
type LeaveRecord struct {
	ID            string    `json:"id"`
	MemberID      string    `json:"member_id"`
	StartDate     time.Time `json:"start_date"`
	EndDate       time.Time `json:"end_date"`
	CoverMemberID string    `json:"cover_member_id"`
	Status        string    `json:"status"`
	LeaveType     string    `json:"leave_type"`
	CreatedAt     time.Time `json:"created_at"`
}

// RotaAssignment is the application-level rota row. The Date
// field is the only string-shaped timestamp in the file (matches
// the rota engine's domain-level "YYYY-MM-DD everywhere" contract).
type RotaAssignment struct {
	ID                   string    `json:"id"`
	Date                 string    `json:"date"`
	MemberID             string    `json:"member_id"`
	IsCover              bool      `json:"is_cover"`
	IsSwapped            bool      `json:"is_swapped"`
	OriginalAssignmentID *string   `json:"original_assignment_id,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	MemberName           string    `json:"member_name,omitempty"`
	MemberEmail          string    `json:"member_email,omitempty"`
}

// CalendarSubscription is the calendar/iCal subscription row,
// paired with a token that the calendar clients use to fetch their
// private feed without authentication.
type CalendarSubscription struct {
	ID                 string     `json:"id"`
	MemberID           string     `json:"member_id"`
	Token              string     `json:"token"`
	CreatedAt          time.Time  `json:"created_at"`
	LastUsedAt         *time.Time `json:"last_used_at,omitempty"`
	LastUsedRotaAt     *time.Time `json:"last_used_rota_at,omitempty"`
	LastUsedMeetingsAt *time.Time `json:"last_used_meetings_at,omitempty"`
}
