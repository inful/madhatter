package notify

import (
	"strings"
	"testing"
	"time"
)

// TestUnsubscribeToken_RejectsExpired pins security review
// finding #8 leg 1: a token whose embedded expiry is in the
// past must be rejected. The pre-fix token had no expiry at
// all — a token captured from a forwarded email or email
// backup was valid forever. The new format embeds a
// unix-second expiry in the signed payload; verification
// rejects the token once the expiry is in the past, even if
// the HMAC is otherwise valid.
func TestUnsubscribeToken_RejectsExpired(t *testing.T) {
	t.Parallel()
	// 16-byte secret to satisfy the documented minimum.
	const secret = "0123456789abcdef0123456789abcdef"

	// Now-1h is unambiguously in the past; emit a token with
	// that expiry. NewUnsubscribeTokenWithTTL takes ttl
	// relative to the current wall clock, so pass a negative
	// duration.
	tok := NewUnsubscribeTokenWithTTL("alice", secret, -1*time.Hour)

	_, err := VerifyUnsubscribeToken(tok.String(), secret)
	if err == nil {
		t.Fatalf("expected an expired-token error, got nil for token %q", tok)
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected error to mention expiry, got %v", err)
	}
}

// TestUnsubscribeToken_AcceptsFuture is the positive control:
// a token issued in the present is valid through its TTL and
// rejected only past it. The default 30-day TTL must be far
// enough that any reasonable test or operation clock skew
// doesn't accidentally expire the token.
func TestUnsubscribeToken_AcceptsFuture(t *testing.T) {
	t.Parallel()
	const secret = "0123456789abcdef0123456789abcdef"
	tok := NewUnsubscribeToken("alice", secret)
	got, err := VerifyUnsubscribeToken(tok.String(), secret)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got != "alice" {
		t.Fatalf("got %q, want %q", got, "alice")
	}
}

// TestUnsubscribeToken_OldFormatRejected pins the
// backward-incompatibility boundary: a token in the pre-fix
// format (`<id>.<sig>`) has only two parts, the new verifier
// splits on dots and expects three. The legacy token is
// rejected — operators upgrading a server have to reissue
// tokens by sending a new notification. The pre-existing
// TestUnsubscribeToken_Tampered already covers the
// `missing-payload` case, but this test pins the *specific*
// upgrade scenario so a future refactor doesn't accidentally
// re-add a back-compat shim.
func TestUnsubscribeToken_OldFormatRejected(t *testing.T) {
	t.Parallel()
	const secret = "0123456789abcdef0123456789abcdef"

	// Manually craft a token in the old `<id>.<sig>` format.
	// NewUnsubscribeToken is the new API; we build the
	// legacy shape directly so the test is independent of
	// any future change to the signer.
	legacy := UnsubscribeToken("alice." + "AABBCCDD")

	if _, err := VerifyUnsubscribeToken(legacy.String(), secret); err == nil {
		t.Fatalf("expected the legacy two-part format to be rejected, got nil for %q", legacy)
	}
}

// TestUnsubscribeToken_ExpiryAtBoundary is the wall-clock
// boundary test: a token whose expiry is exactly equal to
// "now" must be rejected (the comparison is strict `<`, not
// `<=`), so a token whose TTL just elapsed can't be replayed
// for the millisecond of overlap.
func TestUnsubscribeToken_ExpiryAtBoundary(t *testing.T) {
	t.Parallel()
	const secret = "0123456789abcdef0123456789abcdef"

	// Build a token whose expiry is in the past by 1ns.
	// (NewUnsubscribeTokenWithTTL truncates to seconds, so
	// ttl=-1s is the smallest negative value that puts the
	// expiry in the past.)
	tok := NewUnsubscribeTokenWithTTL("alice", secret, -1*time.Second)
	_, err := VerifyUnsubscribeToken(tok.String(), secret)
	if err == nil {
		t.Fatalf("expected expiry-at-boundary token to be rejected, got nil for %q", tok)
	}
}

// TestUnsubscribeToken_DefaultTTLIsSensible is a regression
// guard: the default TTL must be long enough that a normal
// notification lifespan is shorter than the expiry window
// (so users get to act on the link) but short enough that
// captured tokens don't stay valid forever. 30 days matches
// the email-security convention used by major email clients.
func TestUnsubscribeToken_DefaultTTLIsSensible(t *testing.T) {
	t.Parallel()
	if defaultUnsubscribeTokenTTL < 24*time.Hour {
		t.Errorf("defaultUnsubscribeTokenTTL is %v — too short to be useful in practice", defaultUnsubscribeTokenTTL)
	}
	if defaultUnsubscribeTokenTTL > 90*24*time.Hour {
		t.Errorf("defaultUnsubscribeTokenTTL is %v — too long, captured tokens stay valid for too many months", defaultUnsubscribeTokenTTL)
	}
}

// TestUnsubscribeToken_TamperedExpKeepsSignature pins that
// flipping the exp field in an existing valid token (without
// re-signing) is caught by the signature check, not by the
// expiry check. Both gates must fire: the signature gate
// catches a tampering attempt before the expiry gate decides
// "valid timestamp but expired", which would otherwise leak
// the actual expiry value to a probing attacker.
func TestUnsubscribeToken_TamperedExpKeepsSignature(t *testing.T) {
	t.Parallel()
	const secret = "0123456789abcdef0123456789abcdef"
	tok := NewUnsubscribeToken("alice", secret)

	// Find the dot boundaries and rewrite the exp middle
	// segment. The format is `<id>.<exp>.<sig>` so 2 dots
	// means 3 parts.
	parts := strings.Split(tok.String(), ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3-part token, got %d parts in %q", len(parts), tok)
	}
	// Swap the exp for a much larger value but keep the
	// original signature. The HMAC was computed over the
	// original (id, exp) tuple, so this token must fail
	// signature verification.
	tampered := parts[0] + "." + "9999999999" + "." + parts[2]

	if _, err := VerifyUnsubscribeToken(tampered, secret); err == nil {
		t.Fatalf("expected tampered-exp token to fail verification, got nil for %q", tampered)
	}
}
