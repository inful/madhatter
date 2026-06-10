package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNotificationPreferences_DefaultsToEnabled verifies that a
// member with no preference row is treated as email_enabled = true.
// This is the contract every other consumer (ChannelNotifier,
// web handler) relies on for the happy path.
func TestNotificationPreferences_DefaultsToEnabled(t *testing.T) {
	t.Parallel()
	db, err := New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	_, err = db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	m, err := db.GetMemberByEmail(ctx, "alice@example.com")
	require.NoError(t, err)
	require.NotNil(t, m)

	enabled, err := db.IsNotificationEmailEnabled(ctx, m.ID)
	require.NoError(t, err)
	assert.True(t, enabled, "a member with no preference row should be enabled by default")

	pref, err := db.GetNotificationPreference(ctx, m.ID)
	require.NoError(t, err)
	assert.Nil(t, pref, "GetNotificationPreference returns nil for the absence of a row")
}

// TestNotificationPreferences_DisableEnableRoundtrip verifies the
// upsert path used by the unsubscribe web handler.
func TestNotificationPreferences_DisableEnableRoundtrip(t *testing.T) {
	t.Parallel()
	db, err := New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	_, err = db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	m, err := db.GetMemberByEmail(ctx, "alice@example.com")
	require.NoError(t, err)

	// Disable.
	disabledAt := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, db.SetNotificationEmailEnabled(ctx, m.ID, false, &disabledAt))
	enabled, err := db.IsNotificationEmailEnabled(ctx, m.ID)
	require.NoError(t, err)
	assert.False(t, enabled)

	pref, err := db.GetNotificationPreference(ctx, m.ID)
	require.NoError(t, err)
	require.NotNil(t, pref)
	assert.False(t, pref.EmailEnabled)
	require.NotNil(t, pref.DisabledAt)
	assert.WithinDuration(t, disabledAt, *pref.DisabledAt, time.Second)

	// Re-enable (clears disabled_at).
	require.NoError(t, db.SetNotificationEmailEnabled(ctx, m.ID, true, nil))
	enabled, err = db.IsNotificationEmailEnabled(ctx, m.ID)
	require.NoError(t, err)
	assert.True(t, enabled)

	pref, err = db.GetNotificationPreference(ctx, m.ID)
	require.NoError(t, err)
	require.NotNil(t, pref)
	assert.True(t, pref.EmailEnabled)
	assert.Nil(t, pref.DisabledAt, "re-enabling clears disabled_at")
}

// TestNotificationPreferences_CascadeOnMemberDelete verifies the
// foreign-key ON DELETE CASCADE: removing a team member also
// removes their preference row. This is what keeps the table from
// accumulating orphan rows for departed members.
func TestNotificationPreferences_CascadeOnMemberDelete(t *testing.T) {
	t.Parallel()
	db, err := New(":memory:")
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	_, err = db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)
	m, err := db.GetMemberByEmail(ctx, "bob@example.com")
	require.NoError(t, err)

	require.NoError(t, db.SetNotificationEmailEnabled(ctx, m.ID, false, nil))
	pref, err := db.GetNotificationPreference(ctx, m.ID)
	require.NoError(t, err)
	require.NotNil(t, pref)

	require.NoError(t, db.DeleteTeamMember(ctx, m.ID))
	pref, err = db.GetNotificationPreference(ctx, m.ID)
	require.NoError(t, err)
	assert.Nil(t, pref, "preference row should be cascaded away with the member")
}
