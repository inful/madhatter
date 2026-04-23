package calendar

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewICalGenerator(t *testing.T) {
	generator := NewICalGenerator()
	require.NotNil(t, generator)
	require.NotNil(t, generator.calendar)
}

func TestICalGenerator_AddAssignment(t *testing.T) {
	generator := NewICalGenerator()

	assignment := database.RotaAssignment{
		ID:       "test-123",
		Date:     "2026-01-15",
		MemberID: "member-1",
		IsCover:  false,
	}

	err := generator.AddAssignment(assignment, "John Doe")
	require.NoError(t, err)

	// Verify the calendar has one event
	events := generator.calendar.Events()
	assert.Len(t, events, 1)

	// Verify event properties using the calendar serialization
	icalStr, err := generator.Serialize()
	require.NoError(t, err)

	assert.Contains(t, icalStr, "HAT day (John Doe)")
	assert.Contains(t, icalStr, "DTSTART;VALUE=DATE:20260115")
	assert.Contains(t, icalStr, "DTEND;VALUE=DATE:20260116")
	assert.Contains(t, icalStr, "TRANSP:TRANSPARENT")
	assert.Contains(t, icalStr, "X-ALT-DESC;FMTTYPE=text/html")
}

func TestICalGenerator_AddAssignment_IncludesSupportDayLinks(t *testing.T) {
	generator := NewICalGenerator().WithSupportDayLinks(ParseMeetingLinks("Runbook|https://example.com/runbook"))

	assignment := database.RotaAssignment{
		ID:       "test-123",
		Date:     "2026-01-15",
		MemberID: "member-1",
		IsCover:  false,
	}

	err := generator.AddAssignment(assignment, "John Doe")
	require.NoError(t, err)

	icalStr, err := generator.Serialize()
	require.NoError(t, err)

	assert.Contains(t, icalStr, "Links:")
	assert.Contains(t, icalStr, "https://example.com/runbook")
}

func TestICalGenerator_AddCoverAssignment(t *testing.T) {
	generator := NewICalGenerator()

	originalID := "original-123"
	assignment := database.RotaAssignment{
		ID:                   "cover-456",
		Date:                 "2026-01-15",
		MemberID:             "member-2",
		IsCover:              true,
		OriginalAssignmentID: &originalID,
	}

	err := generator.AddAssignment(assignment, "Jane Smith")
	require.NoError(t, err)

	icalStr, err := generator.Serialize()
	require.NoError(t, err)

	// Verify cover indicator in summary
	assert.Contains(t, icalStr, "HAT day (Jane Smith) (COVER)")
	assert.Contains(t, icalStr, "DTSTART;VALUE=DATE:20260115")
	assert.Contains(t, icalStr, "DTEND;VALUE=DATE:20260116")
	assert.Contains(t, icalStr, "TRANSP:TRANSPARENT")
	assert.Contains(t, icalStr, "Support duty (cover)")
}

func TestICalGenerator_AddLeaveEvent(t *testing.T) {
	generator := NewICalGenerator()

	startDate := time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2026, 1, 22, 0, 0, 0, 0, time.UTC)

	err := generator.AddLeaveEvent("John Doe", "vacation", startDate, endDate)
	require.NoError(t, err)

	icalStr, err := generator.Serialize()
	require.NoError(t, err)

	assert.Contains(t, icalStr, "John Doe - Vacation")
	assert.Contains(t, icalStr, "TENTATIVE")
}

func TestICalGenerator_AddHoliday(t *testing.T) {
	generator := NewICalGenerator()

	holidayDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	err := generator.AddHoliday("New Year", holidayDate)
	require.NoError(t, err)

	icalStr, err := generator.Serialize()
	require.NoError(t, err)

	assert.Contains(t, icalStr, "Office Closed - New Year")
	assert.Contains(t, icalStr, "CANCELLED")
}

func TestICalGenerator_Serialize(t *testing.T) {
	generator := NewICalGenerator()

	assignment := database.RotaAssignment{
		ID:       "test-123",
		Date:     "2026-01-15",
		MemberID: "member-1",
		IsCover:  false,
	}

	err := generator.AddAssignment(assignment, "John Doe")
	require.NoError(t, err)

	// Test string serialization
	icalStr, err := generator.Serialize()
	require.NoError(t, err)
	assert.NotEmpty(t, icalStr)
	assert.Contains(t, icalStr, "BEGIN:VCALENDAR")
	assert.Contains(t, icalStr, "BEGIN:VEVENT")
	assert.Contains(t, icalStr, "END:VCALENDAR")

	// Test bytes serialization
	icalBytes, err := generator.SerializeBytes()
	require.NoError(t, err)
	assert.NotEmpty(t, icalBytes)
	assert.Equal(t, []byte(icalStr), icalBytes)
}

func TestGenerateICalFromAssignments(t *testing.T) {
	assignments := []database.RotaAssignment{
		{
			ID:       "assign-1",
			Date:     "2026-01-15",
			MemberID: "member-1",
			IsCover:  false,
		},
		{
			ID:       "assign-2",
			Date:     "2026-01-16",
			MemberID: "member-1",
			IsCover:  false,
		},
	}

	icalStr, err := GenerateICalFromAssignments(assignments, "John Doe")
	require.NoError(t, err)
	assert.NotEmpty(t, icalStr)

	// Should contain both events
	assert.Contains(t, icalStr, "DTSTART;VALUE=DATE:20260115")
	assert.Contains(t, icalStr, "DTSTART;VALUE=DATE:20260116")
}

func TestParseICal(t *testing.T) {
	// Create a simple iCalendar content
	icalContent := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
UID:test-event@domain.com
DTSTART:20260115T090000Z
DTEND:20260115T170000Z
SUMMARY:Test Event
STATUS:CONFIRMED
END:VEVENT
END:VCALENDAR`

	events, err := ParseICal(icalContent)
	require.NoError(t, err)
	assert.Len(t, events, 1)

	event := events[0]
	assert.Equal(t, "test-event@domain.com", event.Id())
}

func TestValidateICal(t *testing.T) {
	validICal := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
UID:test@domain.com
DTSTART:20260115T090000Z
DTEND:20260115T170000Z
SUMMARY:Test
END:VEVENT
END:VCALENDAR`

	err := ValidateICal(validICal)
	require.NoError(t, err)

	invalidICal := "This is not iCalendar content"
	err = ValidateICal(invalidICal)
	require.Error(t, err)
}

func TestICalGenerator_AddTimezoneSupport(t *testing.T) {
	generator := NewICalGenerator()
	generator.AddTimezoneSupport("America/New_York")

	// The method should set the X-WR-TIMEZONE property
	// Note: We can't easily verify this without accessing internal properties
	// but the method should not panic
}

func TestICalGenerator_AddCustomProperty(t *testing.T) {
	generator := NewICalGenerator()

	// This method is a placeholder for future functionality
	// It should not panic when called
	generator.AddCustomProperty("X-CUSTOM-KEY", "custom-value")
}

func TestGenerateTeamCalendar(t *testing.T) {
	assignments := []database.RotaAssignment{
		{
			ID:       "assign-1",
			Date:     "2026-01-15",
			MemberID: "member-1",
			IsCover:  false,
		},
		{
			ID:       "assign-2",
			Date:     "2026-01-15",
			MemberID: "member-2",
			IsCover:  false,
		},
	}

	members := map[string]string{
		"member-1": "Alice",
		"member-2": "Bob",
	}

	icalStr, err := GenerateTeamCalendar(assignments, members)
	require.NoError(t, err)
	assert.NotEmpty(t, icalStr)

	// Should contain both members
	assert.Contains(t, icalStr, "Alice")
	assert.Contains(t, icalStr, "Bob")
}

func TestGenerateOthersICalForToken_ExcludesTokenOwnerAssignments(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	t.Setenv("MIGRATIONS_PATH", filepath.Join(repoRoot, "migrations"))

	ctx := context.Background()
	tmpDir := t.TempDir()
	db, err := database.New(filepath.Join(tmpDir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ownerID, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	otherID, err := db.AddTeamMember(ctx, "Bob", "bob@example.com")
	require.NoError(t, err)

	today := time.Now().Format("2006-01-02")
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	_, err = db.CreateRotaAssignment(ctx, today, ownerID, false, nil)
	require.NoError(t, err)
	_, err = db.CreateRotaAssignment(ctx, tomorrow, otherID, false, nil)
	require.NoError(t, err)

	token, err := db.CreateCalendarSubscription(ctx, ownerID)
	require.NoError(t, err)

	icsStr, err := GenerateOthersICalForToken(ctx, db, token, 7)
	require.NoError(t, err)
	assert.Contains(t, icsStr, "HAT day (Bob)")
	assert.NotContains(t, icsStr, "HAT day (Alice)")
}

func TestICalGenerator_MultipleEvents(t *testing.T) {
	generator := NewICalGenerator()

	// Add multiple assignments
	dates := []string{"2026-01-11", "2026-01-12", "2026-01-13"}
	for i, date := range dates {
		assignment := database.RotaAssignment{
			ID:       "assign-" + string(rune(i+'0')),
			Date:     date,
			MemberID: "member-1",
			IsCover:  false,
		}
		err := generator.AddAssignment(assignment, "John Doe")
		require.NoError(t, err)
	}

	events := generator.calendar.Events()
	assert.Len(t, events, 3)

	// Verify all events are present in serialized output
	icalStr, err := generator.Serialize()
	require.NoError(t, err)

	// Should contain all three dates
	assert.Contains(t, icalStr, "DTSTART;VALUE=DATE:20260111")
	assert.Contains(t, icalStr, "DTSTART;VALUE=DATE:20260112")
	assert.Contains(t, icalStr, "DTSTART;VALUE=DATE:20260113")
}

func TestICalGenerator_InvalidDate(t *testing.T) {
	generator := NewICalGenerator()

	assignment := database.RotaAssignment{
		ID:       "test-123",
		Date:     "invalid-date",
		MemberID: "member-1",
		IsCover:  false,
	}

	err := generator.AddAssignment(assignment, "John Doe")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid date format")
}

func TestICalGenerator_EmptyCalendar(t *testing.T) {
	generator := NewICalGenerator()

	icalStr, err := generator.Serialize()
	require.NoError(t, err)

	// Should still be valid iCalendar even with no events
	assert.Contains(t, icalStr, "BEGIN:VCALENDAR")
	assert.Contains(t, icalStr, "END:VCALENDAR")
}

func TestICalGenerator_CalendarProperties(t *testing.T) {
	generator := NewICalGenerator()

	icalStr, err := generator.Serialize()
	require.NoError(t, err)

	// Verify calendar metadata
	assert.Contains(t, icalStr, "VERSION:2.0")
	assert.Contains(t, icalStr, "PRODID:-//SupportRota//EN")
	assert.Contains(t, icalStr, "CALSCALE:GREGORIAN")
	assert.Contains(t, icalStr, "METHOD:PUBLISH")
	assert.Contains(t, icalStr, "X-WR-CALNAME:Support Rota Calendar")
	// The library uses NAME and DESCRIPTION properties instead of X-WR-CALDESC
	assert.Contains(t, icalStr, "DESCRIPTION:Automated support rota assignments")
	assert.Contains(t, icalStr, "REFRESH-INTERVAL;VALUE=DURATION:PT1H")
	assert.Contains(t, icalStr, "X-PUBLISHED-TTL:PT1H")
}

func TestICalGenerator_ComplexScenario(t *testing.T) {
	generator := NewICalGenerator()

	// Add a regular assignment
	regularAssignment := database.RotaAssignment{
		ID:       "regular-1",
		Date:     "2026-01-15",
		MemberID: "member-1",
		IsCover:  false,
	}
	err := generator.AddAssignment(regularAssignment, "Alice")
	require.NoError(t, err)

	// Add a cover assignment
	originalID := "original-1"
	coverAssignment := database.RotaAssignment{
		ID:                   "cover-1",
		Date:                 "2026-01-16",
		MemberID:             "member-2",
		IsCover:              true,
		OriginalAssignmentID: &originalID,
	}
	err = generator.AddAssignment(coverAssignment, "Bob")
	require.NoError(t, err)

	// Add a leave event
	startDate := time.Date(2026, 1, 17, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2026, 1, 19, 0, 0, 0, 0, time.UTC)
	err = generator.AddLeaveEvent("Charlie", "sick", startDate, endDate)
	require.NoError(t, err)

	// Add a holiday
	holidayDate := time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC)
	err = generator.AddHoliday("Company Holiday", holidayDate)
	require.NoError(t, err)

	// Serialize and verify
	icalStr, err := generator.Serialize()
	require.NoError(t, err)

	// Verify all components are present
	assert.Contains(t, icalStr, "Alice")
	assert.Contains(t, icalStr, "Bob")
	assert.Contains(t, icalStr, "Charlie")
	assert.Contains(t, icalStr, "HAT day")
	assert.Contains(t, icalStr, "Sick")
	assert.Contains(t, icalStr, "Company Holiday")
	assert.Contains(t, icalStr, "CANCELLED")

	// Verify we have 4 events
	events := generator.calendar.Events()
	assert.Len(t, events, 4)
}

func TestICalGenerator_LeaveEventAllDay(t *testing.T) {
	generator := NewICalGenerator()

	// Test that leave events are properly set as all-day
	startDate := time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2026, 1, 22, 0, 0, 0, 0, time.UTC)

	err := generator.AddLeaveEvent("John Doe", "vacation", startDate, endDate)
	require.NoError(t, err)

	icalStr, err := generator.Serialize()
	require.NoError(t, err)

	// Should contain DTSTART and DTEND without time component for all-day events
	// The library handles this internally
	assert.Contains(t, icalStr, "DTSTART;VALUE=DATE:20260120")
	assert.Contains(t, icalStr, "DTEND;VALUE=DATE:20260123") // +1 day after endDate
}

func TestICalGenerator_HolidayAllDay(t *testing.T) {
	generator := NewICalGenerator()

	holidayDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	err := generator.AddHoliday("New Year", holidayDate)
	require.NoError(t, err)

	icalStr, err := generator.Serialize()
	require.NoError(t, err)

	// Should contain all-day event format
	assert.Contains(t, icalStr, "DTSTART;VALUE=DATE:20260101")
	assert.Contains(t, icalStr, "DTEND;VALUE=DATE:20260102")
}

func TestICalGenerator_TransparencySettings(t *testing.T) {
	generator := NewICalGenerator()

	// Add leave event (should be opaque - busy time)
	startDate := time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2026, 1, 22, 0, 0, 0, 0, time.UTC)
	err := generator.AddLeaveEvent("John Doe", "vacation", startDate, endDate)
	require.NoError(t, err)

	// Add holiday (should be transparent - free time)
	holidayDate := time.Date(2026, 1, 21, 0, 0, 0, 0, time.UTC)
	err = generator.AddHoliday("Company Holiday", holidayDate)
	require.NoError(t, err)

	icalStr, err := generator.Serialize()
	require.NoError(t, err)

	// Leave should be opaque
	assert.Contains(t, icalStr, "TRANSP:OPAQUE")
	// Holiday should be transparent
	assert.Contains(t, icalStr, "TRANSP:TRANSPARENT")
}

func TestICalGenerator_SequenceAndModified(t *testing.T) {
	generator := NewICalGenerator()

	assignment := database.RotaAssignment{
		ID:       "test-123",
		Date:     "2026-01-15",
		MemberID: "member-1",
		IsCover:  false,
	}

	err := generator.AddAssignment(assignment, "John Doe")
	require.NoError(t, err)

	icalStr, err := generator.Serialize()
	require.NoError(t, err)

	// Should contain sequence number
	assert.Contains(t, icalStr, "SEQUENCE:0")
	// Should contain last modified timestamp
	assert.Contains(t, icalStr, "LAST-MODIFIED:")
}

func TestICalGenerator_WithAlarm(t *testing.T) {
	generator := NewICalGenerator().WithAlarm()

	assignment := database.RotaAssignment{
		ID:       "test-alarm-123",
		Date:     "2026-01-15",
		MemberID: "member-1",
		IsCover:  false,
	}

	err := generator.AddAssignment(assignment, "John Doe")
	require.NoError(t, err)

	icalStr, err := generator.Serialize()
	require.NoError(t, err)

	assert.Contains(t, icalStr, "BEGIN:VALARM")
	assert.Contains(t, icalStr, "ACTION:DISPLAY")
	assert.Contains(t, icalStr, "TRIGGER:-P1D")
	assert.Contains(t, icalStr, "END:VALARM")
}

func TestICalGenerator_NoAlarmByDefault(t *testing.T) {
	generator := NewICalGenerator()

	assignment := database.RotaAssignment{
		ID:       "test-no-alarm-456",
		Date:     "2026-01-15",
		MemberID: "member-1",
		IsCover:  false,
	}

	err := generator.AddAssignment(assignment, "John Doe")
	require.NoError(t, err)

	icalStr, err := generator.Serialize()
	require.NoError(t, err)

	assert.NotContains(t, icalStr, "BEGIN:VALARM")
}
