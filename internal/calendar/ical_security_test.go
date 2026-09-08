package calendar

import (
	"strings"
	"testing"
	"time"

	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSanitizeICalText_StripsLineBreaks pins the security
// review finding #7 output-sanitization leg: the ICS
// generator must defensively strip CRLF and NUL from any
// user-controlled string before it ends up in an event
// field. The form-layer validation added in web/team_handlers
// is the primary defense; this is a belt-and-braces guard in
// case data slips in via a different path (CSV import,
// future API, direct DB write).
func TestSanitizeICalText_StripsLineBreaks(t *testing.T) {
	cases := []struct {
		name string
		in   string
		out  string
	}{
		{"plain text", "Alice", "Alice"},
		{"LF in middle", "Ali\nce", "Ali ce"},
		{"CRLF in middle", "Ali\r\nce", "Ali ce"},
		{"CR alone", "Ali\rce", "Ali ce"},
		{"NUL in middle", "Ali\x00ce", "Alice"},
		{"multiple newlines", "A\nB\r\nC", "A B C"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.out, sanitizeICalText(tc.in),
				"sanitizeICalText(%q) must produce %q without the line-break / NUL bytes", tc.in, tc.out)
		})
	}
}

// TestAddAssignmentWithSnapshot_StripsControlCharsFromName pins
// the end-to-end behavior: even if a control character reaches
// the ICS generator (bypassing the form-layer check), the
// emitted ICS stream must contain exactly one BEGIN:VEVENT
// line and exactly one END:VEVENT line — CRLF injection would
// inflate those counts. The check counts *line-prefix* matches
// (any line that starts with "BEGIN:VEVENT" or "END:VEVENT"),
// not raw substring matches, because the sanitized payload is
// allowed to contain the substring as data on a different
// line.
func TestAddAssignmentWithSnapshot_StripsControlCharsFromName(t *testing.T) {
	g := NewICalGenerator()
	assignment := database.RotaAssignment{
		ID:   "test-1",
		Date: "2026-01-15",
	}
	// Inject a name with a CRLF — would otherwise produce:
	//   SUMMARY:HAT day (Ali\r\nSUMMARY:forged\r\nBEGIN:VEVENT\r\n...)
	const malicious = "Ali\r\nSUMMARY:forged\r\nBEGIN:VEVENT"
	err := g.AddAssignmentWithSnapshot(assignment, malicious, &presenceSnapshot{Date: "2026-01-15"})
	require.NoError(t, err)

	serialized, err := g.Serialize()
	require.NoError(t, err)

	// Count ICS lines whose first non-whitespace token is
	// BEGIN:VEVENT or END:VEVENT. Anything else is data and
	// is allowed to contain those substrings after the
	// sanitization collapsed the newlines into spaces.
	beginCount := 0
	endCount := 0
	for line := range strings.SplitSeq(serialized, "\n") {
		// The ical library may fold lines by inserting a
		// single space at the start of a continuation line.
		// Strip that and the trailing CR before checking the
		// first token.
		trimmed := strings.TrimRight(line, "\r")
		if strings.HasPrefix(trimmed, "BEGIN:VEVENT") {
			beginCount++
		}
		if strings.HasPrefix(trimmed, "END:VEVENT") {
			endCount++
		}
	}
	assert.Equal(t, 1, beginCount,
		"the ICS must contain exactly one BEGIN:VEVENT line, got %d. The serialized output:\n%s",
		beginCount, serialized)
	assert.Equal(t, 1, endCount,
		"the ICS must contain exactly one END:VEVENT line, got %d", endCount)
}

// TestAddLeaveEventWithSnapshot_StripsControlCharsFromLeaveType
// is the parallel test for the leave event path. A leave_type
// like "sick\r\nSUMMARY:forged" must be sanitized before
// reaching event.Summary.
func TestAddLeaveEventWithSnapshot_StripsControlCharsFromLeaveType(t *testing.T) {
	g := NewICalGenerator()
	err := g.AddLeaveEventWithSnapshot("Alice", "sick\r\nSUMMARY:forged",
		time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 1, 16, 0, 0, 0, 0, time.UTC),
		&presenceSnapshot{Date: "2026-01-15"},
	)
	require.NoError(t, err)

	serialized, err := g.Serialize()
	require.NoError(t, err)

	for line := range strings.SplitSeq(serialized, "\n") {
		assert.NotEqual(t, "SUMMARY:forged", strings.TrimRight(line, "\r"),
			"forged leave summary must not appear on its own line: %q", line)
	}
}
