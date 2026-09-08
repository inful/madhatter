package web

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateTeamMemberInput_RejectsControlCharacters pins
// the security review finding #7 input-validation leg: a
// team member name (or email local-part) containing
// line-break or other control characters must be rejected at
// the form layer. Without the check, a malicious admin could
// inject a CRLF sequence into the team_members row and have
// it propagated verbatim into the ICS calendar feed
// (event.SUMMARY is set with fmt.Sprintf and the library
// doesn't fully sanitize control characters).
//
// The set of rejected characters is the standard C0 control
// range plus the C1 range from 0x80-0x9F, both of which are
// syntactically meaningless in human names and emails.
func TestValidateTeamMemberInput_RejectsControlCharacters(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"LF in name", "Ali\nce"},
		{"CRLF in name", "Ali\r\nce"},
		{"CR in name", "Ali\rce"},
		{"NUL in name", "Ali\x00ce"},
		{"tab in name", "Ali\tce"},
		{"DEL in name", "Ali\x7fce"},
		{"C1 in name", "Ali\xc2\x9fce"}, // U+009F (C1 control) encoded as UTF-8
		{"LF in email local part", "ali\nce@example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateTeamMemberInput(tc.input, "alice@example.com")
			require.Error(t, err,
				"a name containing control characters must be rejected (input=%q)", tc.input)
			assert.True(t,
				strings.Contains(err.Error(), "control") ||
					strings.Contains(err.Error(), "invalid"),
				"the error should mention control characters or be a clear validation error: %v", err)
		})
	}
}

// TestValidateTeamMemberInput_AcceptsUnicodePunctuation pins
// the boundary case: a normal human name with a non-ASCII
// character (the "Ava Müller" pattern) must continue to be
// accepted. The control-character check should not regress
// legitimate international input.
func TestValidateTeamMemberInput_AcceptsUnicodePunctuation(t *testing.T) {
	cases := []string{
		"Ava Müller",
		"José García",
		"李雷",
		"O'Brien",         // ASCII apostrophe
		"Anne-Marie",      // ASCII hyphen
		"Anne\u2009Marie", // narrow no-break space (U+2009)
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			assert.NoError(t, validateTeamMemberInput(name, "alice@example.com"),
				"a name with international characters should be accepted: %q", name)
		})
	}
}
