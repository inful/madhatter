package notify

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeResolver is a minimal RecipientResolver for tests.
type fakeResolver struct {
	byID       map[string]fakeRecipient
	disabled   map[string]bool
	enabledErr error
}

type fakeRecipient struct {
	email string
	name  string
}

func (f fakeResolver) ResolveByID(_ context.Context, memberID string) (string, string, error) {
	r, ok := f.byID[memberID]
	if !ok {
		return "", "", fmt.Errorf("not found: %s", memberID)
	}
	return r.email, r.name, nil
}

// EmailEnabled implements notify.RecipientResolver. The disabled
// map lets tests opt specific members out. A non-nil enabledErr
// simulates a transient DB failure.
func (f fakeResolver) EmailEnabled(_ context.Context, memberID string) (bool, error) {
	if f.enabledErr != nil {
		return true, f.enabledErr
	}
	return !f.disabled[memberID], nil
}

// fakeChannel records every Send call.
type fakeChannel struct {
	mu    sync.Mutex
	name  string
	sent  []outboundMessage
	fail  bool
	calls int
}

func (c *fakeChannel) Name() string { return c.name }
func (c *fakeChannel) Send(_ context.Context, msg outboundMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.fail {
		return errors.New("fake failure")
	}
	c.sent = append(c.sent, msg)
	return nil
}

func (c *fakeChannel) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *fakeChannel) Sent() []outboundMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]outboundMessage, len(c.sent))
	copy(out, c.sent)
	return out
}

func TestLogNotifier_SwapRequested_NotifiesTargetOnly(t *testing.T) {
	email := &fakeChannel{name: "email"}
	resolver := fakeResolver{byID: map[string]fakeRecipient{
		"req": {email: "req@example.com", name: "Requester"},
		"tgt": {email: "tgt@example.com", name: "Target"},
	}}
	n := NewLogNotifier(resolver, nil, email)

	n.SwapRequested(context.Background(), SwapEvent{
		SwapID:            "swap-1",
		RequesterMemberID: "req",
		RequesterName:     "Requester",
		TargetMemberID:    "tgt",
		TargetName:        "Target",
		RequesterDate:     "2026-07-01",
		TargetDate:        "2026-07-15",
	})

	require.Equal(t, 1, email.Calls())
	got := email.Sent()
	assert.Equal(t, "tgt@example.com", got[0].Recipient)
	assert.Equal(t, "Target", got[0].RecipientName)
	assert.Equal(t, EventSwapRequested, got[0].EventKind)
	assert.Contains(t, got[0].Subject, "Requester")
	assert.Contains(t, got[0].Body, "Target")
	assert.Contains(t, got[0].Body, "Requester")
	assert.Contains(t, got[0].Body, "2026-07-01")
	assert.Contains(t, got[0].Body, "2026-07-15")
}

func TestLogNotifier_SwapAccepted_NotifiesBothParties(t *testing.T) {
	email := &fakeChannel{name: "email"}
	resolver := fakeResolver{byID: map[string]fakeRecipient{
		"req": {email: "req@example.com", name: "Requester"},
		"tgt": {email: "tgt@example.com", name: "Target"},
	}}
	n := NewLogNotifier(resolver, nil, email)

	n.SwapAccepted(context.Background(), SwapEvent{
		SwapID:            "swap-1",
		RequesterMemberID: "req",
		RequesterName:     "Requester",
		TargetMemberID:    "tgt",
		TargetName:        "Target",
		RequesterDate:     "2026-07-01",
		TargetDate:        "2026-07-15",
	})

	require.Equal(t, 2, email.Calls())
	recipients := []string{email.Sent()[0].Recipient, email.Sent()[1].Recipient}
	assert.ElementsMatch(t, []string{"req@example.com", "tgt@example.com"}, recipients)
}

func TestLogNotifier_ChannelDisabled_NotCalled(t *testing.T) {
	email := &fakeChannel{name: "email"}
	resolver := fakeResolver{byID: map[string]fakeRecipient{
		"tgt": {email: "tgt@example.com", name: "Target"},
	}}
	// Disable the email channel; the notifier should skip it.
	n := NewLogNotifier(resolver, map[string]bool{ChannelEmail: false}, email)

	n.SwapRequested(context.Background(), SwapEvent{
		SwapID:            "swap-1",
		RequesterMemberID: "req",
		RequesterName:     "Requester",
		TargetMemberID:    "tgt",
		TargetName:        "Target",
		RequesterDate:     "2026-07-01",
		TargetDate:        "2026-07-15",
	})

	assert.Equal(t, 0, email.Calls())
}

func TestLogNotifier_UnknownRecipient_Skipped(t *testing.T) {
	email := &fakeChannel{name: ChannelEmail}
	resolver := fakeResolver{byID: map[string]fakeRecipient{}} // empty
	n := NewLogNotifier(resolver, nil, email)

	n.SwapRequested(context.Background(), SwapEvent{
		SwapID:            "swap-1",
		RequesterMemberID: "req",
		RequesterName:     "Requester",
		TargetMemberID:    "tgt",
		TargetName:        "Target",
		RequesterDate:     "2026-07-01",
		TargetDate:        "2026-07-15",
	})

	assert.Equal(t, 0, email.Calls())
}

func TestLogNotifier_WFHStateChanged_NotifiesRequester(t *testing.T) {
	email := &fakeChannel{name: ChannelEmail}
	resolver := fakeResolver{byID: map[string]fakeRecipient{
		"req": {email: "req@example.com", name: "Requester"},
	}}
	n := NewLogNotifier(resolver, nil, email)

	n.WFHStateChanged(context.Background(), WFHEvent{
		RequestID:  "wfh-1",
		MemberID:   "req",
		MemberName: "Requester",
		Date:       "2026-08-01",
		OldStatus:  "pending",
		NewStatus:  "approved",
		ActorName:  "system",
	})

	require.Equal(t, 1, email.Calls())
	got := email.Sent()[0]
	assert.Equal(t, "req@example.com", got.Recipient)
	assert.Contains(t, got.Subject, "approved")
	assert.Contains(t, got.Body, "2026-08-01")
}

func TestLogNotifier_CoverAssigned_NotifiesCoverMember(t *testing.T) {
	email := &fakeChannel{name: ChannelEmail}
	resolver := fakeResolver{byID: map[string]fakeRecipient{
		"cover": {email: "cover@example.com", name: "Cover"},
	}}
	n := NewLogNotifier(resolver, nil, email)

	n.CoverAssigned(context.Background(), CoverEvent{
		LeaveID:         "leave-1",
		LeaveMemberID:   "alice",
		LeaveMemberName: "Alice",
		CoverMemberID:   "cover",
		CoverMemberName: "Cover",
		StartDate:       "2026-09-01",
		EndDate:         "2026-09-05",
	})

	require.Equal(t, 1, email.Calls())
	got := email.Sent()[0]
	assert.Equal(t, "cover@example.com", got.Recipient)
	assert.Contains(t, got.Subject, "Alice")
	assert.Contains(t, got.Body, "2026-09-01")
	assert.Contains(t, got.Body, "2026-09-05")
}

// TestLogNotifier_DisabledRecipient_Skipped verifies the one-click
// unsubscribe path: when EmailEnabled returns false, the channel
// is not called and no outbox row would be written.
func TestLogNotifier_DisabledRecipient_Skipped(t *testing.T) {
	t.Parallel()
	email := &fakeChannel{name: "email"}
	resolver := fakeResolver{
		byID: map[string]fakeRecipient{
			"req": {email: "req@example.com", name: "Requester"},
			"tgt": {email: "tgt@example.com", name: "Target"},
		},
		disabled: map[string]bool{"tgt": true},
	}
	n := NewLogNotifier(resolver, nil, email)

	n.SwapRequested(context.Background(), SwapEvent{
		SwapID:            "swap-1",
		RequesterMemberID: "req",
		RequesterName:     "Requester",
		TargetMemberID:    "tgt",
		TargetName:        "Target",
		RequesterDate:     "2026-09-01",
		TargetDate:        "2026-09-15",
	})

	assert.Equal(t, 0, email.Calls(), "disabled recipient must not produce a Send call")
}

// TestLogNotifier_PreferenceLookupError_DefaultsEnabled verifies
// the safety net: a transient DB failure during EmailEnabled
// must not silently drop notifications. The notifier logs and
// continues with the message.
func TestLogNotifier_PreferenceLookupError_DefaultsEnabled(t *testing.T) {
	t.Parallel()
	email := &fakeChannel{name: "email"}
	resolver := fakeResolver{
		byID: map[string]fakeRecipient{
			"tgt": {email: "tgt@example.com", name: "Target"},
		},
		enabledErr: errors.New("simulated DB blip"),
	}
	n := NewLogNotifier(resolver, nil, email)

	n.SwapRequested(context.Background(), SwapEvent{
		SwapID:         "swap-1",
		TargetMemberID: "tgt",
		TargetName:     "Target",
		RequesterName:  "Requester",
		RequesterDate:  "2026-09-01",
		TargetDate:     "2026-09-15",
	})

	assert.Equal(t, 1, email.Calls(), "transient preference error must default to enabled, not drop the message")
}
