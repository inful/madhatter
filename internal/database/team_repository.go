package database

import (
	"context"
	"time"
)

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
	// its generated id. Pass nil for birthdate when the admin
	// hasn't recorded one (the dashboard probe excludes nil
	// rows from the celebration banner).
	AddTeamMember(ctx context.Context, name, email string, birthdate *time.Time) (string, error)

	// GetMemberByID returns the member by id.
	GetMemberByID(ctx context.Context, id string) (*TeamMember, error)

	// GetMemberByEmail returns the member by email.
	GetMemberByEmail(ctx context.Context, email string) (*TeamMember, error)

	// GetActiveTeamMembers returns every active member.
	GetActiveTeamMembers(ctx context.Context) ([]TeamMember, error)

	// GetUpcomingBirthdays returns members whose MM-DD falls in
	// [today, today+windowDays], sorted by DaysUntil ascending.
	// Pushing the wrap math into SQL would force a CASE
	// expression; see db.GetUpcomingBirthdays for the rationale.
	GetUpcomingBirthdays(ctx context.Context, today time.Time, windowDays int) ([]UpcomingBirthday, error)

	// UpdateTeamMember renames the member and writes birthdate.
	// Pass nil for birthdate to clear the column back to SQL NULL.
	UpdateTeamMember(ctx context.Context, id, name, email string, birthdate *time.Time) error

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
