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

type LeaveRecord struct {
	ID            string    `json:"id"`
	MemberID      string    `json:"member_id"`
	StartDate     time.Time `json:"start_date"`
	EndDate       time.Time `json:"end_date"`
	CoverMemberID string    `json:"cover_member_id"`
	Status        string    `json:"status"`
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
