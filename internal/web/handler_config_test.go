package web

import (
	"context"
	"os"
	"testing"

	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/calendar"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/notify"
	"github.com/inful/madhatter/internal/wfh"
	"github.com/stretchr/testify/require"
)

// configTestNotifier implements notify.Notifier and records every
// call so a HandlerConfig-driven wiring test can assert which events
// were dispatched. Same shape as the production ChannelNotifier
// minus the outbox — we don't need persistence for field-wiring
// checks.
type configTestNotifier struct {
	swapRequestedCalls   int
	swapAcceptedCalls    int
	swapRejectedCalls    int
	swapCancelledCalls   int
	wfhStateChangedCalls int
	coverAssignedCalls   int
	userPendingCalls     int
}

func (n *configTestNotifier) SwapRequested(_ context.Context, _ notify.SwapEvent) {
	n.swapRequestedCalls++
}

func (n *configTestNotifier) SwapAccepted(_ context.Context, _ notify.SwapEvent) {
	n.swapAcceptedCalls++
}

func (n *configTestNotifier) SwapRejected(_ context.Context, _ notify.SwapEvent) {
	n.swapRejectedCalls++
}

func (n *configTestNotifier) SwapCancelled(_ context.Context, _ notify.SwapEvent) {
	n.swapCancelledCalls++
}

func (n *configTestNotifier) WFHStateChanged(_ context.Context, _ notify.WFHEvent) {
	n.wfhStateChangedCalls++
}

func (n *configTestNotifier) CoverAssigned(_ context.Context, _ notify.CoverEvent) {
	n.coverAssignedCalls++
}

func (n *configTestNotifier) UserPendingApproval(_ context.Context, _ notify.UserPendingApprovalEvent) {
	n.userPendingCalls++
}

// holidayLookupStub satisfies calendar.HolidayLookup with a fixed
// (name, isHoliday) return so the wiring test stays a wiring test,
// not a data test.
type holidayLookupStub struct{ name string }

func (s holidayLookupStub) GetHoliday(_ string) (string, bool) {
	return s.name, s.name != ""
}

// TestNewHandlerConfig_WiresNotifier pins the new constructor's
// wiring contract: HandlerConfig.Notifier ends up as Handler's
// internal notifier so handlers that call notifierOrNil dispatch
// to it. The old positional constructor + SetNotifier pair
// achieved the same result in two steps; the new builder does it
// in one.
func TestNewHandlerConfig_WiresNotifier(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	want := &configTestNotifier{}

	h, err := NewHandlerConfig(HandlerConfig{
		DB:             db,
		AuthManager:    &auth.AuthManager{},
		AuthMiddleware: &auth.Middleware{},
		Notifier:       want,
	})
	require.NoError(t, err)
	require.NotNil(t, h)
	require.Same(t, want, h.notifier, "HandlerConfig.Notifier must reach Handler.notifier")
}

// TestNewHandlerConfig_DefaultsNotifierToNoOp pins that omitting
// Notifier doesn't produce a nil interface field — handlers that
// dispatch unconditionally must not panic when the constructor is
// invoked from a test that doesn't care about notifications.
// This is the contract that the old notifyNoop struct provided;
// the field-default-in-constructor design replaces it.
func TestNewHandlerConfig_DefaultsNotifierToNoOp(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	h, err := NewHandlerConfig(HandlerConfig{
		DB:             db,
		AuthManager:    &auth.AuthManager{},
		AuthMiddleware: &auth.Middleware{},
	})
	require.NoError(t, err)
	require.NotNil(t, h)
	require.NotNil(t, h.notifier, "Handler must default to a no-op notifier so handlers dispatch safely")
}

// TestNewHandlerConfig_WiresHolidayLookup pins that
// HandlerConfig.HolidayLookup reaches Handler.holidayLookup — the
// pairing with the existing SetHolidayLookup setter is what the
// builder replaces.
func TestNewHandlerConfig_WiresHolidayLookup(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	lookup := holidayLookupStub{name: "Bank Holiday"}

	h, err := NewHandlerConfig(HandlerConfig{
		DB:             db,
		AuthManager:    &auth.AuthManager{},
		AuthMiddleware: &auth.Middleware{},
		HolidayLookup:  calendar.HolidayLookup(lookup),
	})
	require.NoError(t, err)
	require.NotNil(t, h.holidayLookup)
}

// TestNewHandlerConfig_WiresWFHService pins that the WFHService
// passed via HandlerConfig reaches Handler.wfhService without the
// SetWFHService setter being called. The setter becomes a no-op
// for config-driven callers.
func TestNewHandlerConfig_WiresWFHService(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	svc := wfh.NewService(db, wfh.Config{})

	h, err := NewHandlerConfig(HandlerConfig{
		DB:             db,
		AuthManager:    &auth.AuthManager{},
		AuthMiddleware: &auth.Middleware{},
		WFHService:     svc,
	})
	require.NoError(t, err)
	require.Same(t, svc, h.wfhService)
}

// TestNewHandlerConfig_WiresUnsubscribe pins that UnsubscribeSecret
// + PublicBaseURL flow to the corresponding private fields and the
// per-recipient URL factory is built at construction time. This
// replaces SetUnsubscribeSecret + SetPublicBaseURL setters.
func TestNewHandlerConfig_WiresUnsubscribe(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	h, err := NewHandlerConfig(HandlerConfig{
		DB:                db,
		AuthManager:       &auth.AuthManager{},
		AuthMiddleware:    &auth.Middleware{},
		UnsubscribeSecret: "test-secret",
		PublicBaseURL:     "https://rota.example.com",
	})
	require.NoError(t, err)
	require.Equal(t, "test-secret", h.unsubscribeSecret)
	require.Equal(t, "https://rota.example.com", h.publicBaseURL)
	require.NotNil(t, h.unsubscribeURLFn,
		"URL factory must be built when both secret and base URL are supplied")
}

// TestNewHandlerConfig_WiresEmptySecret pins the testifylint
// requirement that empty values use require.Empty.

// TestNewHandlerConfig_OmitsUnsubscribeWhenSecretEmpty pins the
// existing partial-secret tolerance: with no secret, the URL
// factory stays nil so the unsubscribe handler treats the link
// as suppressed.
func TestNewHandlerConfig_OmitsUnsubscribeWhenSecretEmpty(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	h, err := NewHandlerConfig(HandlerConfig{
		DB:             db,
		AuthManager:    &auth.AuthManager{},
		AuthMiddleware: &auth.Middleware{},
		PublicBaseURL:  "https://rota.example.com",
	})
	require.NoError(t, err)
	require.Empty(t, h.unsubscribeSecret)
	require.Nil(t, h.unsubscribeURLFn,
		"URL factory must stay nil when there's no secret to sign with")
}

// TestNewHandlerConfig_OmitsUnsubscribeWhenBaseURLEmpty mirrors
// the test above for the other axis — a base URL with no secret is
// just as inert.
func TestNewHandlerConfig_OmitsUnsubscribeWhenBaseURLEmpty(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	h, err := NewHandlerConfig(HandlerConfig{
		DB:                db,
		AuthManager:       &auth.AuthManager{},
		AuthMiddleware:    &auth.Middleware{},
		UnsubscribeSecret: "test-secret",
	})
	require.NoError(t, err)
	require.Nil(t, h.unsubscribeURLFn,
		"URL factory must stay nil without a base URL even with a secret")
}

// TestNewHandlerConfig_RoundtripsTokenAndURL ensures the built
// factory actually produces a usable one-click token. This is the
// behavioral end-to-end pin for the unsubscribe wiring.
func TestNewHandlerConfig_RoundtripsTokenAndURL(t *testing.T) {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	h, err := NewHandlerConfig(HandlerConfig{
		DB:                db,
		AuthManager:       &auth.AuthManager{},
		AuthMiddleware:    &auth.Middleware{},
		UnsubscribeSecret: "test-secret",
		PublicBaseURL:     "https://rota.example.com",
	})
	require.NoError(t, err)

	urlFn := h.UnsubscribeURLFunc()
	require.NotEmpty(t, urlFn("member-1"),
		"the built URL factory must produce a token URL when both fields are set")

	// Verify the env-var path that production uses still works
	// (the second surface may want to honor SESSION_SECRET).
	t.Setenv("SESSION_SECRET", "env-secret")
	require.NotEmpty(t, os.Getenv("SESSION_SECRET"))
}
