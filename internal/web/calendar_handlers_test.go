package web

import (
	"context"
	"crypto/tls"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetSubscriptionURLs_IncludesTeamCalendarLinks(t *testing.T) {
	req := &http.Request{Host: "example.com", Header: make(http.Header)}
	data := map[string]any{}

	setSubscriptionURLs(req, "test-token", data)

	assert.Equal(t, "http://example.com/calendar/test-token/team.ics", data["TeamCalendarURL"])
	assert.Equal(t, "webcal://example.com/calendar/test-token/team.ics", string(data["TeamCalendarWebcalURL"].(template.URL)))
	assert.Contains(t, string(data["TeamCalendarOutlookURL"].(template.URL)), "name=HAT+Days+%28Rest+of+Team%29")
	assert.Contains(t, string(data["TeamCalendarGoogleURL"].(template.URL)), "cid=webcal%3A%2F%2Fexample.com%2Fcalendar%2Ftest-token%2Fteam.ics")
}

func TestBaseURLFromRequest_TLS(t *testing.T) {
	req := &http.Request{Host: "example.com"}
	req.TLS = &tls.ConnectionState{}

	assert.Equal(t, "https://example.com", baseURLFromRequest(req))
}

func TestBaseURLFromRequest_XForwardedProtoAndHost(t *testing.T) {
	req := &http.Request{Host: "internal:8080", Header: make(http.Header)}
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "public.example.com")

	assert.Equal(t, "https://public.example.com", baseURLFromRequest(req))
}

func TestBaseURLFromRequest_Forwarded(t *testing.T) {
	req := &http.Request{Host: "internal:8080", Header: make(http.Header)}
	req.Header.Set("Forwarded", "for=1.2.3.4;proto=https;host=forwarded.example.com")

	assert.Equal(t, "https://forwarded.example.com", baseURLFromRequest(req))
}

func TestOutlookSubscriptionURL_UsesHTTPSAndName(t *testing.T) {
	req := &http.Request{Host: "example.com", Header: make(http.Header)}

	result := outlookSubscriptionURL(req, "/calendar/test-token/ics", "Support rota")
	parsed, err := url.Parse(string(result))
	require.NoError(t, err)
	assert.Equal(t, "https", parsed.Scheme)
	assert.Equal(t, "outlook.office.com", parsed.Host)
	assert.Equal(t, "/calendar/0/addfromweb", parsed.Path)

	query := parsed.Query()
	assert.Equal(t, "Support rota", query.Get("name"))
	assert.Equal(t, "https://example.com/calendar/test-token/ics", query.Get("url"))
	assert.Empty(t, query.Get("path"))
	assert.Empty(t, query.Get("rru"))
}

func TestGoogleSubscriptionURL_UsesCIDWithWebcalURL(t *testing.T) {
	req := &http.Request{Host: "example.com", Header: make(http.Header)}

	result := googleSubscriptionURL(req, "/calendar/test-token/ics")
	parsed, err := url.Parse(string(result))
	require.NoError(t, err)
	assert.Equal(t, "https", parsed.Scheme)
	assert.Equal(t, "calendar.google.com", parsed.Host)
	assert.Equal(t, "/calendar/render", parsed.Path)

	query := parsed.Query()
	assert.Equal(t, "webcal://example.com/calendar/test-token/ics", query.Get("cid"))
}

func newCalendarTestHandler(t *testing.T, db *database.DB) *Handler {
	t.Helper()
	h, err := NewHandler(db, &auth.AuthManager{}, &auth.Middleware{}, false, nil)
	require.NoError(t, err)
	return h
}

// doCalendar issues a GET through the handler's router so chi URL
// params are populated. Returns the recorded response.
func doCalendar(t *testing.T, h *Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rr := httptest.NewRecorder()
	h.Router().ServeHTTP(rr, req)
	return rr
}

func TestHandleMeetingsDayHTML_BusinessDayRendersMeeting(t *testing.T) {
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()
	h := newCalendarTestHandler(t, db)

	ctx := context.Background()
	_, err := db.AddTeamMember(ctx, "Alice", "alice@example.com")
	require.NoError(t, err)
	memberID, err := db.AddTeamMember(ctx, "Token Owner", "token@example.com")
	require.NoError(t, err)
	token, err := db.CreateCalendarSubscription(ctx, memberID)
	require.NoError(t, err)
	_ = ctx

	// Tuesday 2026-01-13 is a business day.
	rr := doCalendar(t, h, "/calendar/"+token+"/meetings/2026-01-13.html")

	assert.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	body := rr.Body.String()
	assert.Contains(t, body, "Morning Shuffle")
	assert.Contains(t, body, "2026-01-13")
}

func TestHandleMeetingsDayHTML_WeekendShowsEmptyState(t *testing.T) {
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()
	h := newCalendarTestHandler(t, db)

	ctx := context.Background()
	memberID, err := db.AddTeamMember(ctx, "Token Owner", "token@example.com")
	require.NoError(t, err)
	token, err := db.CreateCalendarSubscription(ctx, memberID)
	require.NoError(t, err)
	_ = ctx

	// Saturday 2026-01-17 is a weekend.
	rr := doCalendar(t, h, "/calendar/"+token+"/meetings/2026-01-17.html")

	assert.Equal(t, http.StatusOK, rr.Code, "body=%s", rr.Body.String())
	assert.Contains(t, rr.Body.String(), "No meetings scheduled")
}

func TestHandleMeetingsDayHTML_InvalidDateReturns400(t *testing.T) {
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()
	h := newCalendarTestHandler(t, db)

	ctx := context.Background()
	memberID, err := db.AddTeamMember(ctx, "Token Owner", "token@example.com")
	require.NoError(t, err)
	token, err := db.CreateCalendarSubscription(ctx, memberID)
	require.NoError(t, err)
	_ = ctx

	rr := doCalendar(t, h, "/calendar/"+token+"/meetings/not-a-date.html")

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleMeetingsDayHTML_UnknownTokenReturns404(t *testing.T) {
	db, cleanup := setupSwapTestDB(t)
	defer cleanup()
	h := newCalendarTestHandler(t, db)

	rr := doCalendar(t, h, "/calendar/no-such-token/meetings/2026-01-13.html")

	assert.Equal(t, http.StatusNotFound, rr.Code)
}
