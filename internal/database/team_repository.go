package database

import "context"

// TeamRepository is the per-aggregate repository contract for the
// team-member data plane. Pulling these methods off the *DB god
// object lets callers (handlers, services, tests) depend on the
// narrow surface they actually use, and makes future test doubles
// straightforward.
//
// This is the first step of the per-aggregate repository split
// (recommendation #1). *DB satisfies the interface today; future
// commits will migrate call sites off *DB.
// type checks. The compile-time assertion below fails the build if
// DB drifts out of compliance with the contract.
type TeamRepository interface {
	// AddTeamMember creates a new active team member and returns
	// its generated id.
	AddTeamMember(ctx context.Context, name, email string) (string, error)

	// GetMemberByID returns the member by id.
	GetMemberByID(ctx context.Context, id string) (*TeamMember, error)

	// GetMemberByEmail returns the member by email.
	GetMemberByEmail(ctx context.Context, email string) (*TeamMember, error)

	// GetActiveTeamMembers returns every active member.
	GetActiveTeamMembers(ctx context.Context) ([]TeamMember, error)

	// UpdateTeamMember renames the member.
	UpdateTeamMember(ctx context.Context, id, name, email string) error

	// DeleteTeamMember removes the member.
	DeleteTeamMember(ctx context.Context, id string) error

	// SetTeamMemberPermanentWFH toggles the all-weekdays-on flag.
	SetTeamMemberPermanentWFH(ctx context.Context, id string, isPermanentWFH bool) error

	// SetTeamMemberExemptFromAssignment toggles the picker-exempt flag.
	SetTeamMemberExemptFromAssignment(ctx context.Context, id string, exempt bool) error

	// SetTeamMemberRecurringWFHDays writes the bitmask-shape view
	// to the five per-weekday flags.
	SetTeamMemberRecurringWFHDays(ctx context.Context, id string, days RecurringWFHDays) error
}

// Compile-time assertion: *DB must satisfy TeamRepository. Any
// future signature drift on the team-member methods will break
// this line before it can break callers.
var _ TeamRepository = (*DB)(nil)
