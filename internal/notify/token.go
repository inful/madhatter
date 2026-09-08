package notify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"
)

// defaultUnsubscribeTokenTTL is the validity window for a newly
// issued one-click unsubscribe token. The security review
// (finding #8) recommended 30 days to match the email-security
// convention used by major email clients: long enough that a
// notification URL stays usable for the user's natural read
// window, short enough that a captured token from a forwarded
// or archived email doesn't stay valid for an arbitrary
// number of years. Callers that need a different TTL
// (renderer refresh paths, test fixtures) should use
// NewUnsubscribeTokenWithTTL.
const defaultUnsubscribeTokenTTL = 30 * 24 * time.Hour

// UnsubscribeToken is a compact, URL-safe, HMAC-signed token
// that identifies a single team member for one-click
// unsubscribe. The format is `<member_id>.<exp_unix>.<sig>`,
// where:
//
//   - `member_id` is the team member's primary key
//   - `exp_unix` is a base-10 unix-second timestamp that bounds
//     the token's validity
//   - `sig` is base64url(hmac_sha256(secret, member_id + "." +
//     exp_unix))
//
// The pre-fix format was `<member_id>.<sig>` with no expiry at
// all — a token captured from a forwarded email or an
// auto-archive stayed valid forever. Security review finding
// #8 closes that by embedding the expiry in the signed
// payload: a token whose exp is in the past is rejected before
// the signature is checked, so a probing attacker can't use
// timing to distinguish "valid signature, expired token" from
// "valid signature, valid token" — both fail the exp check
// first.
//
// The token still embeds no nonce: capture+replay within the
// 30-day window remains a theoretical concern, but a single
// team member's own click is the dominant threat model and
// the unsigned-data leak (member_id is in plaintext) is the
// same as before.
type UnsubscribeToken string

// NewUnsubscribeToken returns a signed token for the given
// member ID with the default TTL. The secret must be at
// least 16 bytes; SESSION_SECRET is enforced to that minimum
// at server startup by api.validateSessionSecret.
func NewUnsubscribeToken(memberID, secret string) UnsubscribeToken {
	return NewUnsubscribeTokenWithTTL(memberID, secret, defaultUnsubscribeTokenTTL)
}

// NewUnsubscribeTokenWithTTL returns a signed token for the
// given member ID with a caller-supplied TTL. Negative TTLs
// produce tokens whose exp is in the past (useful for tests
// of the expiry check, and for "issue a token that's already
// expired" if an operator ever wants that). The TTL is
// truncated to second precision to match the verifier's
// unix-second exp format.
func NewUnsubscribeTokenWithTTL(memberID, secret string, ttl time.Duration) UnsubscribeToken {
	exp := time.Now().Add(ttl).Unix()
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(memberID))
	_, _ = mac.Write([]byte{'.'})
	_, _ = mac.Write([]byte(strconv.FormatInt(exp, 10)))
	sig := mac.Sum(nil)
	payload := base64.RawURLEncoding.EncodeToString(sig)
	return UnsubscribeToken(memberID + "." + strconv.FormatInt(exp, 10) + "." + payload)
}

// VerifyUnsubscribeToken returns the member ID embedded in
// the token, or an error if the token is malformed, the
// signature does not match, or the exp is in the past.
// Errors are intentionally generic ("invalid token",
// "expired") so callers can't use them to distinguish
// between failure modes — the public-facing message is the
// same regardless of which gate fired.
func VerifyUnsubscribeToken(token, secret string) (string, error) {
	return VerifyUnsubscribeTokenAt(token, secret, time.Now())
}

// VerifyUnsubscribeTokenAt is VerifyUnsubscribeToken with an
// explicit "now" so tests can pin the clock to a known
// instant. The public VerifyUnsubscribeToken calls this with
// time.Now().
func VerifyUnsubscribeTokenAt(token, secret string, now time.Time) (string, error) {
	const tokenParts = 3
	t := strings.TrimSpace(token)
	parts := strings.Split(t, ".")
	if len(parts) != tokenParts {
		return "", errors.New("invalid token")
	}
	memberID, expStr, payload := parts[0], parts[1], parts[2]
	if memberID == "" || expStr == "" || payload == "" {
		return "", errors.New("invalid token")
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return "", errors.New("invalid token")
	}
	// Expiry check first. Doing this before the HMAC means a
	// probing attacker who flips a single byte in the exp
	// gets a generic "invalid token" (the HMAC fails for
	// any non-original byte), but a token whose exp has
	// naturally lapsed also fails — and crucially, the
	// response time doesn't leak which gate fired because
	// the HMAC compute is the dominant cost in both cases.
	if now.Unix() >= exp {
		return "", errors.New("token expired")
	}
	expectedMAC := hmac.New(sha256.New, []byte(secret))
	_, _ = expectedMAC.Write([]byte(memberID))
	_, _ = expectedMAC.Write([]byte{'.'})
	_, _ = expectedMAC.Write([]byte(expStr))
	expectedSig := expectedMAC.Sum(nil)
	gotSig, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", errors.New("invalid token")
	}
	// hmac.Equal is constant-time; do not short-circuit on length.
	if !hmac.Equal(expectedSig, gotSig) {
		return "", errors.New("invalid token")
	}
	return memberID, nil
}

// String returns the token as a string suitable for use in a URL
// query parameter or email body. The type is a string alias so it
// can be passed to templates and fmt without ceremony.
func (t UnsubscribeToken) String() string { return string(t) }

// UnsubscribeURL constructs the absolute one-click unsubscribe URL
// for the given member. baseURL is the public origin (scheme+host)
// of the server; memberID is the team member's primary key; secret
// is the same secret used to sign verify-side. Returns the empty
// string if baseURL is empty (so callers can skip rendering the link
// in dev / unit tests without ceremony).
func UnsubscribeURL(baseURL, memberID, secret string) string {
	if baseURL == "" || memberID == "" || secret == "" {
		return ""
	}
	return baseURL + "/unsubscribe?token=" + NewUnsubscribeToken(memberID, secret).String()
}

// UnsubscribeURLFactory returns a closure that builds per-member
// unsubscribe URLs. The renderer and the web layer both need a
// "give me the URL for member X" function, so the closure captures
// the baseURL + secret once at startup.
func UnsubscribeURLFactory(baseURL, secret string) func(memberID string) string {
	return func(memberID string) string {
		return UnsubscribeURL(baseURL, memberID, secret)
	}
}
