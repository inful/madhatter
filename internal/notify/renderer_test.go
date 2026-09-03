package notify

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//nolint:goconst // test fixtures intentionally reuse sample names and dates
func TestRenderer_RendersAllEventKinds(t *testing.T) {
	r, err := newRenderer("https://rota.example.com", nil)
	require.NoError(t, err)

	cases := []struct {
		name        string
		kind        string
		event       any
		wantSubject []string // substrings the subject must contain
		wantBody    []string // substrings the body must contain
	}{
		{
			name: "swap.requested",
			kind: EventSwapRequested,
			event: SwapEvent{
				RequesterName: "Alice", TargetName: "Bob",
				RequesterDate: "2026-07-01", TargetDate: "2026-07-15",
			},
			wantSubject: []string{"Alice"},
			wantBody:    []string{"Alice", "Bob", "2026-07-01", "2026-07-15", "https://rota.example.com/swaps"},
		},
		{
			name: "swap.accepted",
			kind: EventSwapAccepted,
			event: SwapEvent{
				RequesterName: "Alice", TargetName: "Bob",
				RequesterDate: "2026-07-01", TargetDate: "2026-07-15",
			},
			wantSubject: []string{"Bob"},
			wantBody:    []string{"Alice", "Bob"},
		},
		{
			name: "swap.rejected",
			kind: EventSwapRejected,
			event: SwapEvent{
				RequesterName: "Alice", TargetName: "Bob",
				RequesterDate: "2026-07-01", TargetDate: "2026-07-15",
			},
			wantSubject: []string{"Bob"},
			wantBody:    []string{"Bob", "Alice"},
		},
		{
			name: "swap.cancelled",
			kind: EventSwapCancelled,
			event: SwapEvent{
				RequesterName: "Alice", TargetName: "Bob",
				RequesterDate: "2026-07-01", TargetDate: "2026-07-15",
			},
			wantSubject: []string{"Alice"},
			wantBody:    []string{"Alice", "Bob"},
		},
		{
			name: "wfh.state_changed",
			kind: EventWFHStateChange,
			event: WFHEvent{
				Date:      "2026-08-01",
				OldStatus: "pending",
				NewStatus: "approved",
				ActorName: "system",
			},
			wantSubject: []string{"2026-08-01", "approved"},
			wantBody:    []string{"2026-08-01", "pending", "approved", "system"},
		},
		{
			name: "cover.assigned",
			kind: EventCoverAssigned,
			event: CoverEvent{
				LeaveMemberName: "Alice",
				StartDate:       "2026-09-01",
				EndDate:         "2026-09-05",
			},
			wantSubject: []string{"Alice", "2026-09-01"},
			wantBody:    []string{"Alice", "2026-09-01", "2026-09-05"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subject, body, err := r.render(tc.kind, tc.event, "")
			require.NoError(t, err)
			assert.NotEmpty(t, subject)
			assert.NotEmpty(t, body)
			for _, s := range tc.wantSubject {
				assert.Contains(t, subject, s,
					"subject %q missing substring %q", subject, s)
			}
			for _, s := range tc.wantBody {
				assert.Contains(t, body, s,
					"body missing substring %q", s)
			}
		})
	}
}

func TestRenderer_OverrideViaEnvVar(t *testing.T) {
	dir := t.TempDir()
	override := dir + "/swap.requested.subject.tmpl"
	require.NoError(t, writeFile(override, "OVERRIDE: {{.RequesterName}} is asking you"))

	t.Setenv("NOTIFY_SWAP_REQUESTED_SUBJECT_TXT_PATH", override)

	r, err := newRenderer("https://x", nil)
	require.NoError(t, err)

	subject, _, err := r.render(EventSwapRequested, SwapEvent{
		RequesterName: "Alice", TargetName: "Bob",
		RequesterDate: "2026-07-01", TargetDate: "2026-07-15",
	}, "")
	require.NoError(t, err)
	assert.Equal(t, "OVERRIDE: Alice is asking you", subject)
}

func TestRenderer_OverrideFileMissing_ReturnsError(t *testing.T) {
	t.Setenv("NOTIFY_SWAP_REQUESTED_SUBJECT_TXT_PATH", "/no/such/file")

	_, err := newRenderer("https://x", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "NOTIFY_SWAP_REQUESTED_SUBJECT_TXT_PATH")
}

// TestRenderer_UnsubscribeURL_AppliesPerRecipient verifies that the
// per-recipient unsubscribe URL is injected into body templates when
// the renderer is constructed with a URL function. The function
// receives the recipient's member_id and returns the absolute URL.
func TestRenderer_UnsubscribeURL_AppliesPerRecipient(t *testing.T) {
	t.Parallel()
	urlFn := func(memberID string) string {
		return "https://example.com/unsubscribe?token=" + memberID
	}
	r, err := newRenderer("https://example.com", urlFn)
	require.NoError(t, err)

	_, body, err := r.render(EventSwapRequested, SwapEvent{
		RequesterName: "Alice",
		TargetName:    "Bob",
		RequesterDate: "2026-07-01",
		TargetDate:    "2026-07-15",
	}, "alice-id")
	require.NoError(t, err)
	assert.Contains(t, body, "https://example.com/unsubscribe?token=alice-id")
	assert.Contains(t, body, "To stop receiving these emails:")
}

// TestRenderer_UnsubscribeURL_NilFnLeavesFooterBlank verifies that
// when no URL function is configured (e.g. in --development mode
// where the unsubscribe secret is empty), the body still renders
// but the footer block is suppressed.
func TestRenderer_UnsubscribeURL_NilFnLeavesFooterBlank(t *testing.T) {
	t.Parallel()
	r, err := newRenderer("https://example.com", nil)
	require.NoError(t, err)

	_, body, err := r.render(EventSwapRequested, SwapEvent{
		RequesterName: "Alice",
		TargetName:    "Bob",
		RequesterDate: "2026-07-01",
		TargetDate:    "2026-07-15",
	}, "alice-id")
	require.NoError(t, err)
	assert.NotContains(t, body, "To stop receiving these emails:")
}

// TestRenderer_SwapTemplatesAreKindNeutral pins Step 20's
// documentation: the swap email bodies must NOT call out "HAT
// day" specifically. A WFH swap uses the same four event kinds
// (SwapRequested / Accepted / Rejected / Cancelled) as the
// HAT-day swap path, so the rendered prose must describe "a
// day" generically. A future drift that reintroduces "HAT" in
// the templates would fail this test rather than silently
// mis-label a WFH-swap email.
//
// Pinned substrings:
//   - subject MUST NOT contain "HAT"
//   - body MUST NOT contain "HAT"
func TestRenderer_SwapTemplatesAreKindNeutral(t *testing.T) {
	t.Parallel()
	r, err := newRenderer("https://rota.example.com", nil)
	require.NoError(t, err)

	cases := []struct {
		name  string
		kind  string
		event SwapEvent
	}{
		{"swap.requested", EventSwapRequested, SwapEvent{
			RequesterName: "Alice", TargetName: "Bob",
			RequesterDate: "2026-07-01", TargetDate: "2026-07-15",
		}},
		{"swap.accepted", EventSwapAccepted, SwapEvent{
			RequesterName: "Alice", TargetName: "Bob",
			RequesterDate: "2026-07-01", TargetDate: "2026-07-15",
		}},
		{"swap.rejected", EventSwapRejected, SwapEvent{
			RequesterName: "Alice", TargetName: "Bob",
			RequesterDate: "2026-07-01", TargetDate: "2026-07-15",
		}},
		{"swap.cancelled", EventSwapCancelled, SwapEvent{
			RequesterName: "Alice", TargetName: "Bob",
			RequesterDate: "2026-07-01", TargetDate: "2026-07-15",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			subject, body, err := r.render(c.kind, c.event, "alice-id")
			require.NoError(t, err)
			assert.NotContains(t, subject, "HAT",
				"%s subject must not surface 'HAT'", c.name)
			assert.NotContains(t, body, "HAT",
				"%s body must not surface 'HAT'", c.name)
		})
	}
}

func TestRenderer_EnvKeyFor(t *testing.T) {
	cases := map[string]string{
		EventSwapRequested:       "NOTIFY_SWAP_REQUESTED_TXT_PATH",
		EventSwapAccepted:        "NOTIFY_SWAP_ACCEPTED_TXT_PATH",
		EventSwapRejected:        "NOTIFY_SWAP_REJECTED_TXT_PATH",
		EventSwapCancelled:       "NOTIFY_SWAP_CANCELLED_TXT_PATH",
		EventWFHStateChange:      "NOTIFY_WFH_STATE_CHANGED_TXT_PATH",
		EventCoverAssigned:       "NOTIFY_COVER_ASSIGNED_TXT_PATH",
		EventUserPendingApproval: "NOTIFY_USER_PENDING_APPROVAL_TXT_PATH",
	}
	for kind, want := range cases {
		assert.Equal(t, want, envKeyFor(kind, "body"),
			"envKeyFor(%q, body)", kind)
		assert.Equal(t, want[:len(want)-len("_TXT_PATH")]+"_SUBJECT_TXT_PATH",
			envKeyFor(kind, "subject"),
			"envKeyFor(%q, subject)", kind)
	}
}

// writeFile is a tiny helper to keep the override test self-contained.
func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}
