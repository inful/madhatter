package database

import "time"

// HatSwap represents a request to swap HAT day assignments between
// two team members. Distinct from WFHAssignmentSwap (which swaps
// WFH-day duties, not HAT-day duties); the two are kept on
// separate type names so callers can't accidentally cross the
// streams at the database level.
//
// Note: this is the application-level shape, populated by
// GetEnrichedSwaps via member/assignment name lookups. The
// sqlc-generated counterpart in
// internal/database/sqlc/hat_swaps.sql.go is the storage shape.
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
