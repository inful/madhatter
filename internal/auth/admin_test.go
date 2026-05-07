package auth

import (
	"database/sql"
	"testing"

	"github.com/inful/madhatter/internal/database/sqlc"
	"github.com/stretchr/testify/assert"
)

func TestIsAdmin(t *testing.T) {
	t.Run("Invalid Flag", func(t *testing.T) {
		assert.False(t, IsAdmin(sql.NullInt64{}))
	})

	t.Run("Non Admin", func(t *testing.T) {
		assert.False(t, IsAdmin(sql.NullInt64{Int64: 0, Valid: true}))
	})

	t.Run("Admin", func(t *testing.T) {
		assert.True(t, IsAdmin(sql.NullInt64{Int64: 1, Valid: true}))
	})
}

func TestIsAdminSession(t *testing.T) {
	t.Run("Nil Session", func(t *testing.T) {
		assert.False(t, IsAdminSession(nil))
	})

	t.Run("Non Admin Session", func(t *testing.T) {
		session := &sqlc.GetSessionByTokenRow{
			IsAdmin: sql.NullInt64{Int64: 0, Valid: true},
		}

		assert.False(t, IsAdminSession(session))
	})

	t.Run("Admin Session", func(t *testing.T) {
		session := &sqlc.GetSessionByTokenRow{
			IsAdmin: sql.NullInt64{Int64: 1, Valid: true},
		}

		assert.True(t, IsAdminSession(session))
	})
}
