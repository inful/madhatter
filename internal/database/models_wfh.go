package database

import "time"

// WFHStatusPending, WFHStatusApproved, WFHStatusDenied,
// WFHStatusCancelled, and WFHStatusWithdrawn are the closed set of
// values wfh_requests.status accepts. The DB enforces the same set
// via a CHECK constraint (migration 000022); these constants let
// the application reject bad values before they reach the driver.
//
// The WFHSwapStatus constants below are intentionally separate: a
// swap has its own state machine (pending → accepted | rejected |
// cancelled) and conflating the two would force callers to write
// "WFHStatus == WFHSwapStatus* everywhere" rather than pick the
// right type.
const (
	WFHStatusPending   = "pending"
	WFHStatusApproved  = "approved"
	WFHStatusDenied    = "denied"
	WFHStatusCancelled = "cancelled"
	WFHStatusWithdrawn = "withdrawn"
)

// WFHSwapStatus is the status of a WFH assignment swap. The state
// machine is pending → accepted | rejected | cancelled. Cancellation
// has two paths: the requester voluntarily cancels, or the
// scheduler's auto-cancel pass flips pending swaps whose swap_date
// is in the past (step 15 of plans/assigned-wfh-plan.md).
type WFHSwapStatus string

const (
	WFHSwapStatusPending   WFHSwapStatus = "pending"
	WFHSwapStatusAccepted  WFHSwapStatus = "accepted"
	WFHSwapStatusRejected  WFHSwapStatus = "rejected"
	WFHSwapStatusCancelled WFHSwapStatus = "cancelled"
)

// WFHRequest represents a work-from-home day request for a team
// member. It is the application-level shape — the sqlc-generated
// counterpart in internal/database/sqlc/wfh_requests.sql.go is
// translated by WFHRequestFromSQLC (in wfh_crud.go).
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
	// Origin describes how the row came to exist. The picker, the
	// quota counter, the API, the calendar, and the WFH list page
	// all branch on this so they can distinguish a self-requested
	// WFH from a system-assigned one, a contractual recurring day,
	// or a swap-target's accepted transfer.
	//
	//   ad_hoc     — self-requested via the WFH request form
	//   recurring  — auto-inserted by the recurring-WFH materializer
	//   assigned   — auto-inserted by the seat-cap picker
	//   swap       — created when a swap request was accepted
	Origin string `json:"origin"`
	// DenialReason is the human-readable explanation for why a
	// request was denied. Set by the settlement path when the row
	// flips to status=denied; surfaced on the dashboard, the WFH
	// list page, the admin manage page, and the email notification
	// so the user is never left guessing why a request was rejected.
	DenialReason *string `json:"denial_reason,omitempty"`
	// Enriched fields (populated by callers).
	MemberName string `json:"member_name,omitempty"`
}

// WFHCoPresence records that two members were on-site together on
// a working day. The seat-cap picker reads this for the
// co-presence tiebreaker — "haven't been on-site with the cohort
// recently" lowers a candidate's priority so the picker keeps them
// on-site to meet the cohort.
//
// Rows are unordered pairs in canonical ordering
// (MemberIDA < MemberIDB) — halves the row count and removes the
// symmetric-pair problem. The CHECK constraint at the storage
// layer enforces this; the writer is responsible for ordering
// before inserting.
//
// Migration 000027 introduces the table and three indexes. The
// retention prune (step 11 of plans/assigned-wfh-plan.md) keeps
// rows bounded to WFH_COPRESENCE_RETENTION_DAYS.
type WFHCoPresence struct {
	ID          string    `json:"id"`
	WorkingDate string    `json:"working_date"` // "2006-01-02"
	MemberIDA   string    `json:"member_id_a"`
	MemberIDB   string    `json:"member_id_b"`
	RecordedAt  time.Time `json:"recorded_at"`
}

// WFHAssignmentSwap represents a request to swap a seat-cap-
// picker-assigned WFH day with an on-site teammate. The cap stays
// met across the swap: the original assigned row is withdrawn
// (status='withdrawn', withdrawn_by='swap:<id>') and a new row is
// inserted for the target with origin='swap'. Single-transaction
// update.
//
// Phase 3 of plans/assigned-wfh-plan.md. Migration 000025
// introduces the table; the swap routes and form land in step 14.
type WFHAssignmentSwap struct {
	ID                    string     `json:"id"`
	RequesterWFHRequestID string     `json:"requester_wfh_request_id"`
	TargetMemberID        string     `json:"target_member_id"`
	SwapDate              string     `json:"swap_date"` // "2006-01-02"
	Status                string     `json:"status"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
	ResolvedAt            *time.Time `json:"resolved_at,omitempty"`
	// Enriched fields (populated by callers; not stored).
	RequesterName string `json:"requester_name,omitempty"`
	TargetName    string `json:"target_name,omitempty"`
	WFHOrigin     string `json:"wfh_origin,omitempty"`
}
