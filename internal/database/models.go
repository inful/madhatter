package database

import "time"

type TeamMember struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
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
