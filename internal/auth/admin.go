package auth

import (
	"database/sql"

	"github.com/inful/madhatter/internal/database/sqlc"
)

// IsAdmin returns true when an admin flag is present and set to 1.
func IsAdmin(adminFlag sql.NullInt64) bool {
	return adminFlag.Valid && adminFlag.Int64 == 1
}

// IsAdminSession returns true when the session belongs to an admin user.
func IsAdminSession(session *sqlc.GetSessionByTokenRow) bool {
	if session == nil {
		return false
	}

	return IsAdmin(session.IsAdmin)
}
