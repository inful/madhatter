package database

import "time"

type RecurringWFHDays struct {
	Monday    bool
	Tuesday   bool
	Wednesday bool
	Thursday  bool
	Friday    bool
}

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
	IsPermanentWFH bool      `json:"is_permanent_wfh"`
	CreatedAt      time.Time `json:"created_at"`
}

// IsRecurringWFHOn reports whether the member has a contractual recurring WFH day on date.
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

// HasPermanentRecurringWFH reports whether all weekdays are configured as recurring WFH.
func (m TeamMember) HasPermanentRecurringWFH() bool {
	return m.RecurringWFHMonday &&
		m.RecurringWFHTuesday &&
		m.RecurringWFHWednesday &&
		m.RecurringWFHThursday &&
		m.RecurringWFHFriday
}

// HasAnyRecurringWFH reports whether the member has at least one recurring
// WFH weekday configured. Used by the materializer as a fast-path skip.
func (m TeamMember) HasAnyRecurringWFH() bool {
	return m.RecurringWFHMonday ||
		m.RecurringWFHTuesday ||
		m.RecurringWFHWednesday ||
		m.RecurringWFHThursday ||
		m.RecurringWFHFriday
}

// LeaveTypeLeave and LeaveTypeConference are the closed set of values
// leave_records.leave_type accepts. The DB enforces the same set via a
// CHECK constraint (migration 000021); keeping the constants here lets
// the application reject bad values before they reach the driver and
// gives call sites a typed comparison instead of raw string equality.
const (
	LeaveTypeLeave      = "leave"
	LeaveTypeConference = "conference"
)

// IsValidLeaveType reports whether v is one of the accepted leave types.
// Callers should use this when accepting leave_type from user input so
// the application rejects the same set the DB CHECK constraint would.
func IsValidLeaveType(v string) bool {
	switch v {
	case LeaveTypeLeave, LeaveTypeConference:
		return true
	default:
		return false
	}
}

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

type RotaAssignment struct {
	ID                   string    `json:"id"`
	Date                 string    `json:"date"`
	MemberID             string    `json:"member_id"`
	IsCover              bool      `json:"is_cover"`
	IsSwapped            bool      `json:"is_swapped"`
	OriginalAssignmentID *string   `json:"original_assignment_id"`
	CreatedAt            time.Time `json:"created_at"`
	MemberName           string    `json:"member_name,omitempty"`
	MemberEmail          string    `json:"member_email,omitempty"`
}

type CalendarSubscription struct {
	ID                 string     `json:"id"`
	MemberID           string     `json:"member_id"`
	Token              string     `json:"token"`
	CreatedAt          time.Time  `json:"created_at"`
	LastUsedAt         *time.Time `json:"last_used_at,omitempty"`
	LastUsedRotaAt     *time.Time `json:"last_used_rota_at,omitempty"`
	LastUsedMeetingsAt *time.Time `json:"last_used_meetings_at,omitempty"`
}

// WFHRequest represents a work-from-home day request for a team member.
type WFHRequest struct {
	ID          string     `json:"id"`
	MemberID    string     `json:"member_id"`
	Date        string     `json:"date"` // "2006-01-02"
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	SettledAt   *time.Time `json:"settled_at,omitempty"`
	WithdrawnBy *string    `json:"withdrawn_by,omitempty"`
	WithdrawnAt *time.Time `json:"withdrawn_at,omitempty"`
	// IsRecurring is true when the row was auto-approved by the recurring-WFH
	// materializer from a member's contractual weekday schedule.
	IsRecurring bool `json:"is_recurring"`
	// IsAdminMarked is true when the row was inserted by an admin via
	// the "Mark WFH" override (wfh.Service.MarkWFH). The chip in the
	// dashboard renders in a distinct color so the team can see at a
	// glance which days were admin-asserted rather than requested.
	IsAdminMarked bool       `json:"is_admin_marked"`
	MarkedBy      *string    `json:"marked_by,omitempty"`
	MarkedAt      *time.Time `json:"marked_at,omitempty"`
	// DenialReason is the human-readable explanation for why a
	// request was denied. Set by the settlement path when the row
	// flips to status=denied; surfaced on the dashboard, the WFH
	// list page, the admin manage page, and the email notification
	// so the user is never left guessing why a request was rejected.
	DenialReason *string `json:"denial_reason,omitempty"`
	// Enriched fields (populated by callers).
	MemberName string `json:"member_name,omitempty"`
}

// WFH status constants.
const (
	WFHStatusPending   = "pending"
	WFHStatusApproved  = "approved"
	WFHStatusDenied    = "denied"
	WFHStatusCancelled = "cancelled"
	WFHStatusWithdrawn = "withdrawn"
)

// HatSwap represents a request to swap HAT day assignments between two team members.
type HatSwap struct {
	ID                    string    `json:"id"`
	RequesterAssignmentID string    `json:"requester_assignment_id"`
	TargetAssignmentID    string    `json:"target_assignment_id"`
	RequesterMemberID     string    `json:"requester_member_id"`
	TargetMemberID        string    `json:"target_member_id"`
	Status                string    `json:"status"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
	// Enriched fields (populated by GetEnrichedSwaps).
	RequesterName string `json:"requester_name,omitempty"`
	TargetName    string `json:"target_name,omitempty"`
	RequesterDate string `json:"requester_date,omitempty"`
	TargetDate    string `json:"target_date,omitempty"`
}
