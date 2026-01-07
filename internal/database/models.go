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
	StartDate     string    `json:"start_date"`
	EndDate       string    `json:"end_date"`
	Type          string    `json:"type"`
	CoverMemberID string    `json:"cover_member_id"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
}

type RotaAssignment struct {
	ID          string    `json:"id"`
	Date        string    `json:"date"`
	MemberID    string    `json:"member_id"`
	IsCover     bool      `json:"is_cover"`
	LeaveID     *string   `json:"leave_id"`
	CreatedAt   time.Time `json:"created_at"`
	MemberName  string    `json:"member_name,omitempty"`
	MemberEmail string    `json:"member_email,omitempty"`
}

type CalendarSubscription struct {
	ID        string    `json:"id"`
	MemberID  string    `json:"member_id"`
	Token     string    `json:"token"`
	CreatedAt time.Time `json:"created_at"`
}
