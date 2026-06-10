package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/inful/madhatter/internal/database/sqlc"
)

// NotificationPreference is the in-Go representation of a row in
// notification_preferences. It mirrors the SQLC-generated struct but
// uses plain Go types (bool, *time.Time) so callers don't have to
// deal with sql.NullInt64 / sql.NullTime for the common case.
type NotificationPreference struct {
	MemberID     string
	EmailEnabled bool
	DisabledAt   *time.Time
	UpdatedAt    time.Time
}

// GetNotificationPreference returns the preference row for a member.
// Returns (nil, nil) when the member has never changed their
// preference — the absence of a row means "default" (email enabled).
// A real error is returned only when the underlying query fails.
func (db *DB) GetNotificationPreference(ctx context.Context, memberID string) (*NotificationPreference, error) {
	row, err := db.queries.GetNotificationPreference(ctx, memberID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return notificationPreferenceFromSQLC(row), nil
}

// IsNotificationEmailEnabled returns true when the member has not
// disabled email. Returns true by default when no preference row
// exists. The COALESCE in the underlying query makes the return
// type interface{}; we scan it through int64 to recover the bit.
func (db *DB) IsNotificationEmailEnabled(ctx context.Context, memberID string) (bool, error) {
	v, err := db.queries.IsNotificationEmailEnabled(ctx, memberID)
	if err != nil {
		return false, err
	}
	switch x := v.(type) {
	case int64:
		return x != 0, nil
	case int:
		return x != 0, nil
	case float64:
		return x != 0, nil
	case bool:
		return x, nil
	case nil:
		return true, nil
	default:
		return false, fmt.Errorf("notification: unexpected IsEnabled return type %T", v)
	}
}

// SetNotificationEmailEnabled upserts the email-enabled flag for a
// member. Pass enabled=false to unsubscribe (and set disabledAt to
// time.Now()), enabled=true to resume (disabledAt=nil). The caller
// is responsible for choosing the timestamp so they can record the
// right clock for tests.
func (db *DB) SetNotificationEmailEnabled(ctx context.Context, memberID string, enabled bool, disabledAt *time.Time) error {
	var enabledInt int64
	if enabled {
		enabledInt = 1
	}
	var da sql.NullTime
	if disabledAt != nil {
		da = sql.NullTime{Time: disabledAt.UTC(), Valid: true}
	}
	return db.queries.SetNotificationEmailEnabled(ctx, sqlc.SetNotificationEmailEnabledParams{
		MemberID:     memberID,
		EmailEnabled: enabledInt,
		DisabledAt:   da,
	})
}

func notificationPreferenceFromSQLC(r sqlc.NotificationPreference) *NotificationPreference {
	p := &NotificationPreference{
		MemberID:     r.MemberID,
		EmailEnabled: r.EmailEnabled != 0,
		UpdatedAt:    r.UpdatedAt.UTC(),
	}
	if r.DisabledAt.Valid {
		t := r.DisabledAt.Time.UTC()
		p.DisabledAt = &t
	}
	return p
}
