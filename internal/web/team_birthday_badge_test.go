package web

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTeamList_BirthdayBadge renders the team.html template
// against a hand-built data map (Members slice + the
// surrounding context) and asserts the 🎂 badge shows up
// next to a member who has a birthdate on file. Pinned by
// #60.
func TestTeamList_BirthdayBadge_RendersForMemberWithBirthdate(t *testing.T) {
	db, _, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	bday := time.Date(1990, 5, 15, 0, 0, 0, 0, time.UTC)
	_, err := db.AddTeamMember(t.Context(), "Alice", "alice@example.com", &bday)
	require.NoError(t, err)

	members, err := db.GetActiveTeamMembers(t.Context())
	require.NoError(t, err)
	require.Len(t, members, 1)

	tmpl, err := parseTemplates()
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	require.NoError(t, tmpl.ExecuteTemplate(rec, "team.html", map[string]any{
		"Template":             "team",
		"Tab":                  "all",
		"Members":              members,
		"Users":                []any{},
		"PendingUsers":         []any{},
		"DeactivatedUsers":     []any{},
		"SubscriptionActivity": map[string]any{},
	}))
	body := rec.Body.String()

	assert.Contains(t, body, "Alice",
		"the team list must still render the member's name")
	assert.Contains(t, body, "🎂",
		"the team list must show the cake emoji next to the member with a birthdate")
	assert.Contains(t, body, `class="birthday-badge"`,
		"the cake emoji must carry the birthday-badge class for styling hooks")
}

// TestTeamList_BirthdayBadge_HidesForMembersWithoutBirthdate
// pins the privacy default: members who haven't shared a
// birthdate get no badge. The dashboard probe (covered
// separately in dashboard_birthday_test.go) also excludes
// null birthdates, so the team-list badge and the dashboard
// banner agree.
func TestTeamList_BirthdayBadge_HidesForMembersWithoutBirthdate(t *testing.T) {
	db, _, cleanup := setupLeaveTestDB(t)
	defer cleanup()

	_, err := db.AddTeamMember(t.Context(), "Bob", "bob@example.com", nil)
	require.NoError(t, err)

	members, err := db.GetActiveTeamMembers(t.Context())
	require.NoError(t, err)
	require.Len(t, members, 1)
	assert.Nil(t, members[0].Birthdate,
		"sanity: Bob has no birthdate on file")

	tmpl, err := parseTemplates()
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	require.NoError(t, tmpl.ExecuteTemplate(rec, "team.html", map[string]any{
		"Template":             "team",
		"Tab":                  "all",
		"Members":              members,
		"Users":                []any{},
		"PendingUsers":         []any{},
		"DeactivatedUsers":     []any{},
		"SubscriptionActivity": map[string]any{},
	}))
	body := rec.Body.String()

	assert.Contains(t, body, "Bob",
		"the team list must still render the member's name")
	assert.NotContains(t, body, "🎂",
		"the team list must NOT show the cake emoji for members without a birthdate")
	assert.NotContains(t, body, `class="birthday-badge"`,
		"the birthday-badge class must not appear for members without a birthdate")
}
