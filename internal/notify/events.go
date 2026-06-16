package notify

// SwapEvent is the data the renderer needs to produce a swap notification
// email. The recipient is not included; the Notifier resolves it from
// RequesterMemberID / TargetMemberID by looking up team_members.email.
type SwapEvent struct {
	SwapID            string
	RequesterMemberID string
	RequesterName     string
	TargetMemberID    string
	TargetName        string
	RequesterDate     string // "2006-01-02"
	TargetDate        string // "2006-01-02"
	ActorName         string // who performed the action
	Reason            string // optional, e.g. denial reason
}

// WFHEvent is the data for a WFH request state change notification.
// OldStatus / NewStatus are WFHStatus* constants from the database package.
type WFHEvent struct {
	RequestID  string
	MemberID   string
	MemberName string
	Date       string // "2006-01-02"
	OldStatus  string
	NewStatus  string
	ActorName  string // "system" for settlement, admin name for admin withdraw
}

// CoverEvent is the data for a "you have been assigned a HAT day" email.
// The renderer produces one message covering the full date range; we do
// not send a separate email per day.
type CoverEvent struct {
	LeaveID         string
	LeaveMemberID   string
	LeaveMemberName string
	CoverMemberID   string
	CoverMemberName string
	StartDate       string // "2006-01-02"
	EndDate         string // "2006-01-02"
	// ResolvedBy is "leave" (created from a leave report) or "leave_edit"
	// (created when a leave was edited and the cover rotated).
	ResolvedBy string
}

// UserPendingApprovalEvent fires when a new user is created by the
// OAuth callback. The notifier resolves every active admin and
// emails them a link to the team page so they can approve or deny
// the new account. Until an admin approves, the user cannot log in.
type UserPendingApprovalEvent struct {
	UserID    string
	UserName  string
	UserEmail string
	Provider  string
	// CreatedAt is an ISO-8601 timestamp rendered in the email so
	// the admin can see how long the request has been waiting.
	CreatedAt string
}
