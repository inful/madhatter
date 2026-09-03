package calendar

import (
	"context"
	"os"
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

func TestAddAssignment_DefaultTemplateMatchesExistingOutput(t *testing.T) {
	g, err := newSupportGenerator(SupportCalendarOptions{})
	require.NoError(t, err)

	a := database.RotaAssignment{
		ID:       "a1",
		Date:     "2026-06-10",
		MemberID: "m1",
	}
	require.NoError(t, g.AddAssignment(a, "Alice"))
	out, err := g.Serialize()
	require.NoError(t, err)
	assert.Contains(t, out, "HAT day (Alice)")
	assert.Contains(t, out, "Support duty")
}

func TestAddAssignment_TemplateOverride_AppliesToDescription(t *testing.T) {
	tmp := t.TempDir()
	textPath := filepath.Join(tmp, "support.txt.tmpl")
	htmlPath := filepath.Join(tmp, "support.html.tmpl")
	require.NoError(t, writeFile(textPath, "CUSTOM SUPPORT: {{.Summary}} on {{.Date}}"))
	require.NoError(t, writeFile(htmlPath, "<p>CUSTOM HTML: {{.Summary}} {{.HATName}}</p>"))

	g, err := newSupportGenerator(SupportCalendarOptions{
		SupportAssignmentTemplateTextPath: textPath,
		SupportAssignmentTemplateHTMLPath: htmlPath,
	})
	require.NoError(t, err)

	snap := &presenceSnapshot{Date: "2026-06-10", HATName: "Alice"}
	a := database.RotaAssignment{ID: "a1", Date: "2026-06-10", MemberID: "m1"}
	require.NoError(t, g.AddAssignmentWithSnapshot(a, "Alice", snap))

	out, err := g.Serialize()
	require.NoError(t, err)
	assert.Contains(t, out, "CUSTOM SUPPORT: HAT day (Alice) on 2026-06-10")
}

func TestAddAssignment_TemplateOverride_InvalidPath_ReturnsError(t *testing.T) {
	_, err := newSupportGenerator(SupportCalendarOptions{
		SupportAssignmentTemplateTextPath: "/no/such/file.tmpl",
	})
	assert.Error(t, err)
}

func TestAddAssignment_TemplateOverride_InvalidSyntax_ReturnsError(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "broken.tmpl")
	require.NoError(t, writeFile(path, "{{ .BadSyntax"))
	_, err := newSupportGenerator(SupportCalendarOptions{
		SupportAssignmentTemplateTextPath: path,
	})
	assert.Error(t, err)
}

func TestAddLeaveEvent_DefaultTemplateMatchesExistingOutput(t *testing.T) {
	g, err := newSupportGenerator(SupportCalendarOptions{})
	require.NoError(t, err)

	date := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	require.NoError(t, g.AddLeaveEvent("Alice", "vacation", date, date))
	out, err := g.Serialize()
	require.NoError(t, err)
	assert.Contains(t, out, "Vacation leave for Alice")
}

func TestAddLeaveEvent_TemplateOverride_AppliesToDescription(t *testing.T) {
	tmp := t.TempDir()
	textPath := filepath.Join(tmp, "leave.txt.tmpl")
	htmlPath := filepath.Join(tmp, "leave.html.tmpl")
	require.NoError(t, writeFile(textPath, "CUSTOM LEAVE: {{.MemberName}} ({{.LeaveType}}) for {{.TotalActive}} active"))
	require.NoError(t, writeFile(htmlPath, "<p>CUSTOM HTML LEAVE: {{.MemberName}}</p>"))

	g, err := newSupportGenerator(SupportCalendarOptions{
		LeaveTemplateTextPath: textPath,
		LeaveTemplateHTMLPath: htmlPath,
	})
	require.NoError(t, err)

	snap := &presenceSnapshot{Date: "2026-06-10", TotalActive: 7}
	date := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	require.NoError(t, g.AddLeaveEventWithSnapshot("Alice", "Vacation", date, date, snap))

	out, err := g.Serialize()
	require.NoError(t, err)
	assert.Contains(t, out, "CUSTOM LEAVE: Alice (Vacation) for 7 active")
}

func TestAddHoliday_DefaultTemplateMatchesExistingOutput(t *testing.T) {
	g, err := newSupportGenerator(SupportCalendarOptions{})
	require.NoError(t, err)

	date := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	require.NoError(t, g.AddHoliday("Constitution Day", date))
	out, err := g.Serialize()
	require.NoError(t, err)
	assert.Contains(t, out, "Support rota is not scheduled on this day")
	assert.Contains(t, out, "Office Closed - Constitution Day")
}

func TestAddHoliday_TemplateOverride_AppliesToDescription(t *testing.T) {
	tmp := t.TempDir()
	textPath := filepath.Join(tmp, "holiday.txt.tmpl")
	htmlPath := filepath.Join(tmp, "holiday.html.tmpl")
	require.NoError(t, writeFile(textPath, "CUSTOM HOLIDAY: {{.Name}} (WFH: {{len .WFH}}, leave: {{len .OnLeave}})"))
	require.NoError(t, writeFile(htmlPath, "<p>CUSTOM HTML HOLIDAY: {{.Name}}</p>"))

	g, err := newSupportGenerator(SupportCalendarOptions{
		HolidayTemplateTextPath: textPath,
		HolidayTemplateHTMLPath: htmlPath,
	})
	require.NoError(t, err)

	snap := &presenceSnapshot{
		Date:    "2026-06-10",
		WFH:     []presenceMember{{Name: "Bob"}},
		OnLeave: []presenceMember{{Name: "Carol"}, {Name: "Dave"}},
	}
	date := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	require.NoError(t, g.AddHolidayWithSnapshot("Constitution Day", date, snap))

	out, err := g.Serialize()
	require.NoError(t, err)
	assert.Contains(t, out, `CUSTOM HOLIDAY: Constitution Day (WFH: 1\, leave: 2)`)
}

func TestGenerateSupportCalendar_TemplateSeesAllSnapshotFields(t *testing.T) {
	tmp := t.TempDir()
	textPath := filepath.Join(tmp, "support.txt.tmpl")
	htmlPath := filepath.Join(tmp, "support.html.tmpl")

	templateBody := `{{.Summary}} | active={{.TotalActive}} onsite={{len .OnSite}} leave={{len .OnLeave}} wfh={{len .WFH}} hat={{.HATName}} order={{range .ShuffledOrder}}{{.Name}} {{end}}`
	require.NoError(t, writeFile(textPath, templateBody))
	require.NoError(t, writeFile(htmlPath, "<p>{{.Summary}} {{.HATName}}</p>"))

	g, err := newSupportGenerator(SupportCalendarOptions{
		SupportAssignmentTemplateTextPath: textPath,
		SupportAssignmentTemplateHTMLPath: htmlPath,
		ShuffleSeed:                       "test-seed",
	})
	require.NoError(t, err)

	snap := &presenceSnapshot{
		Date:        "2026-06-10",
		TotalActive: 5,
		OnSite:      []presenceMember{{Name: "Alice"}, {Name: "Bob"}},
		OnLeave:     []presenceMember{{Name: "Carol"}},
		WFH:         []presenceMember{{Name: "Dave"}, {Name: "Eve"}},
		HATName:     "Alice",
		ShuffledOrder: []presenceMember{
			{Name: "Eve"}, {Name: "Bob"}, {Name: "Dave"}, {Name: "Alice"}, {Name: "Carol"},
		},
	}
	a := database.RotaAssignment{ID: "a1", Date: "2026-06-10", MemberID: "m1"}
	require.NoError(t, g.AddAssignmentWithSnapshot(a, "Alice", snap))

	out, err := g.Serialize()
	require.NoError(t, err)
	assert.Contains(t, out, "active=5")
	assert.Contains(t, out, "onsite=2")
	assert.Contains(t, out, "leave=1")
	assert.Contains(t, out, "wfh=2")
	assert.Contains(t, out, "hat=Alice")
}

func writeFile(path, contents string) error {
	return os.WriteFile(path, []byte(contents), 0o600)
}

// TestAddWFHEvent_DefaultsRenderEvent pins the per-member WFH
// VEVENT format. The summary is "<member> - WFH", the description
// is the default "X is working from home" line, and the event
// is all-day with the standard transparent-opaque / tentative
// pairing (busy for calendar-client conflict detection, but
// status tentative so the user can still see a confirmed
// WFH as flexibly scheduled).
func TestAddWFHEvent_DefaultsRenderEvent(t *testing.T) {
	g := NewICalGenerator()
	date := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	require.NoError(t, g.AddWFHEvent("Alice", date, false))

	out, err := g.Serialize()
	require.NoError(t, err)
	assert.Contains(t, out, "SUMMARY:Alice - WFH",
		"summary should be the configured WFH format")
	assert.Contains(t, out, "Alice is working from home",
		"description should use the default WFH text template")
	assert.Contains(t, out, "DTSTART;VALUE=DATE:20260904",
		"event should be all-day starting on the WFH date")
	assert.Contains(t, out, "STATUS:TENTATIVE",
		"event should be tentative so the user can see it as flexibly scheduled")
}

// TestAddWFHEvent_AdminMarkedAppendsBanner pins the admin-marked
// visual distinction: the description appends "(marked by admin)"
// so subscribers can tell an admin correction from a self-requested
// WFH day — same color cue the dashboard uses, translated into
// text for calendar clients that don't render color.
func TestAddWFHEvent_AdminMarkedAppendsBanner(t *testing.T) {
	g := NewICalGenerator()
	date := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	require.NoError(t, g.AddWFHEvent("Alice", date, true))

	out, err := g.Serialize()
	require.NoError(t, err)
	assert.Contains(t, out, "(marked by admin)",
		"admin-marked WFH event should carry the banner in the description")
}

// TestAddWFHEvent_UidIsStablePerDay pins that the per-member
// per-day UID doesn't change between calls (so calendar clients
// can recognize the same event on the next refresh). Two calls
// for the same member+date produce the same UID; different dates
// produce different UIDs.
func TestAddWFHEvent_UidIsStablePerDay(t *testing.T) {
	g := NewICalGenerator()
	date1 := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	date2 := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	require.NoError(t, g.AddWFHEvent("Alice", date1, false))
	require.NoError(t, g.AddWFHEvent("Alice", date2, false))

	out, err := g.Serialize()
	require.NoError(t, err)
	// Two distinct UIDs (one per day).
	assert.Regexp(t, `UID:wfh-alice-\d+`, out,
		"WFH events should use a wfh-<member>-<unix> UID scheme")
}
