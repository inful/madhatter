package web

import (
	"context"
	"database/sql"
	"net/http"
	"testing"

	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database/sqlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMustGetUser_UserInContext_ReturnsUser(t *testing.T) {
	row := &sqlc.GetSessionByTokenRow{
		Email:   "alice@example.com",
		Name:    "Alice",
		IsAdmin: sql.NullInt64{Int64: 0, Valid: true},
	}

	ctx := context.WithValue(context.Background(), auth.UserContextKey, row)
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	require.NoError(t, err)

	got := mustGetUser(r.Context())

	assert.Equal(t, "alice@example.com", got.Email)
	assert.Equal(t, "Alice", got.Name)
}

func TestMustGetUser_NoUserInContext_Panics(t *testing.T) {
	r, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	require.NoError(t, err)

	assert.Panics(t, func() {
		mustGetUser(r.Context())
	})
}
