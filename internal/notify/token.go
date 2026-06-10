package notify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

// UnsubscribeToken is a compact, URL-safe, HMAC-signed token that
// identifies a single team member for one-click unsubscribe. The format
// is `<member_id>.<base64url(payload)>`, where the payload is
// `<base64url(hmac_sha256(secret, member_id))>`. There is no expiry —
// the token is invalidated either by rotating the secret or by the
// member re-enabling notifications (the handler re-enables by reading
// the token, so a token consumed once is still a valid form of
// authentication for the re-enable flow). The token intentionally
// embeds no timestamp or nonce: the scope is "who is asking", not
// "when was this issued", so the simpler format is sufficient.
type UnsubscribeToken string

// NewUnsubscribeToken returns a signed token for the given member ID.
// secret must be at least 16 bytes; in production it is derived from
// SESSION_SECRET, which is enforced at server startup.
func NewUnsubscribeToken(memberID, secret string) UnsubscribeToken {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(memberID))
	sig := mac.Sum(nil)
	payload := base64.RawURLEncoding.EncodeToString(sig)
	return UnsubscribeToken(memberID + "." + payload)
}

// VerifyUnsubscribeToken returns the member ID embedded in token, or
// an error if the token is malformed or the signature does not match.
// Errors are intentionally generic ("invalid token") so callers can't
// use them to distinguish between "no member" and "bad signature".
func VerifyUnsubscribeToken(token, secret string) (string, error) {
	const tokenParts = 2
	t := strings.TrimSpace(token)
	parts := strings.Split(t, ".")
	if len(parts) != tokenParts {
		return "", errors.New("invalid token")
	}
	memberID, payload := parts[0], parts[1]
	if memberID == "" || payload == "" {
		return "", errors.New("invalid token")
	}
	expectedMAC := hmac.New(sha256.New, []byte(secret))
	_, _ = expectedMAC.Write([]byte(memberID))
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
