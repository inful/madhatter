package notify

import (
	"strings"
	"testing"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func TestUnsubscribeToken_Roundtrip(t *testing.T) {
	t.Parallel()
	cases := []string{"alice-123", "bob", "x", strings.Repeat("a", 64)}
	for _, id := range cases {
		id := id
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			tok := NewUnsubscribeToken(id, testSecret)
			got, err := VerifyUnsubscribeToken(tok.String(), testSecret)
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if got != id {
				t.Fatalf("got %q, want %q", got, id)
			}
		})
	}
}

func TestUnsubscribeToken_Tampered(t *testing.T) {
	t.Parallel()
	tok := NewUnsubscribeToken("alice", testSecret)
	cases := []struct {
		name, mutated string
	}{
		{"empty", ""},
		{"missing-payload", "alice."},
		{"missing-id", "." + tok.String()[strings.Index(tok.String(), ".")+1:]},
		{"wrong-id", "bob." + tok.String()[strings.Index(tok.String(), ".")+1:]},
		{"truncated-payload", "alice.AAAA"},
		{"no-dot", "alice"},
		{"extra-dot", "alice.foo.bar"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := VerifyUnsubscribeToken(tc.mutated, testSecret); err == nil {
				t.Fatalf("expected error for %q", tc.mutated)
			}
		})
	}
}

func TestUnsubscribeToken_WrongSecret(t *testing.T) {
	t.Parallel()
	tok := NewUnsubscribeToken("alice", testSecret)
	if _, err := VerifyUnsubscribeToken(tok.String(), "different-secret-but-long-enough"); err == nil {
		t.Fatalf("expected error with different secret")
	}
}

func TestUnsubscribeURL_RequiresAllInputs(t *testing.T) {
	t.Parallel()
	if got := UnsubscribeURL("https://example.com", "alice", ""); got != "" {
		t.Errorf("expected empty string for empty secret, got %q", got)
	}
	if got := UnsubscribeURL("", "alice", "secret"); got != "" {
		t.Errorf("expected empty string for empty baseURL, got %q", got)
	}
	if got := UnsubscribeURL("https://example.com", "", "secret"); got != "" {
		t.Errorf("expected empty string for empty memberID, got %q", got)
	}
	url := UnsubscribeURL("https://example.com", "alice", testSecret)
	if !strings.HasPrefix(url, "https://example.com/unsubscribe?token=") {
		t.Errorf("unexpected URL: %q", url)
	}
	// Round-trip the URL token.
	tokenStr := strings.TrimPrefix(url, "https://example.com/unsubscribe?token=")
	got, err := VerifyUnsubscribeToken(tokenStr, testSecret)
	if err != nil {
		t.Fatalf("verify URL token: %v", err)
	}
	if got != "alice" {
		t.Errorf("verify got %q, want %q", got, "alice")
	}
}
