package web

import (
	"html/template"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/calendar"
)

const maxCalendarFormBytes = 1 << 20

// setSubscriptionURLs populates the calendar/meetings URL template data for a given token.
func setSubscriptionURLs(r *http.Request, token string, data map[string]any) {
	data["Token"] = token
	data["CalendarURL"] = baseURLFromRequest(r) + "/calendar/" + token + "/ics"
	data["TeamCalendarURL"] = baseURLFromRequest(r) + "/calendar/" + token + "/team.ics"
	data["MeetingsCalendarURL"] = baseURLFromRequest(r) + "/calendar/" + token + "/meetings.ics"
	data["CalendarWebcalURL"] = webcalSubscriptionURL(r, "/calendar/"+token+"/ics")
	data["TeamCalendarWebcalURL"] = webcalSubscriptionURL(r, "/calendar/"+token+"/team.ics")
	data["MeetingsCalendarWebcalURL"] = webcalSubscriptionURL(r, "/calendar/"+token+"/meetings.ics")
	data["CalendarOutlookURL"] = outlookSubscriptionURL(r, "/calendar/"+token+"/ics", "HAT Days")
	data["TeamCalendarOutlookURL"] = outlookSubscriptionURL(r, "/calendar/"+token+"/team.ics", "HAT Days (Rest of Team)")
	data["MeetingsCalendarOutlookURL"] = outlookSubscriptionURL(r, "/calendar/"+token+"/meetings.ics", "Shuffles")
	data["CalendarGoogleURL"] = googleSubscriptionURL(r, "/calendar/"+token+"/ics")
	data["TeamCalendarGoogleURL"] = googleSubscriptionURL(r, "/calendar/"+token+"/team.ics")
	data["MeetingsCalendarGoogleURL"] = googleSubscriptionURL(r, "/calendar/"+token+"/meetings.ics")
}

// handleCalendarAdminPost handles the admin-only POST to create a subscription for another member.
func (h *Handler) handleCalendarAdminPost(w http.ResponseWriter, r *http.Request, data map[string]any) {
	r.Body = http.MaxBytesReader(w, r.Body, maxCalendarFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	token, err := h.db.CreateCalendarSubscription(r.Context(), r.PostForm.Get("member_id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	setSubscriptionURLs(r, token, data)
	data["AdminCreated"] = true

	members, err := h.db.GetActiveTeamMembers(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data["Members"] = members

	if err := h.tmpl.ExecuteTemplate(w, "calendar.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// loadUserSubscription resolves the logged-in user's team member record and populates
// subscription URL data, auto-creating a subscription if none exists.
func (h *Handler) loadUserSubscription(r *http.Request, email string, data map[string]any) {
	member, err := h.db.GetMemberByEmail(r.Context(), email)
	if err != nil {
		return
	}

	subs, err := h.db.GetSubscriptionsByMemberID(r.Context(), member.ID)
	if err != nil || len(subs) == 0 {
		token, createErr := h.db.CreateCalendarSubscription(r.Context(), member.ID)
		if createErr == nil {
			setSubscriptionURLs(r, token, data)
		}

		return
	}

	setSubscriptionURLs(r, subs[0].Token, data)
}

func (h *Handler) handleCalendar(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	isAdmin := false
	data := map[string]any{
		"Template": "calendar",
	}

	user, loggedIn := auth.GetUserFromContext(ctx)
	if loggedIn {
		data["User"] = user
		isAdmin = auth.IsAdminSession(user)
		data["IsAdmin"] = isAdmin
	}

	if r.Method == http.MethodPost {
		if !isAdmin {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		h.handleCalendarAdminPost(w, r, data)
		return
	}

	if loggedIn {
		h.loadUserSubscription(r, user.Email, data)
	}

	if isAdmin {
		members, err := h.db.GetActiveTeamMembers(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		data["Members"] = members
	}

	if err := h.tmpl.ExecuteTemplate(w, "calendar.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func webcalSubscriptionURL(r *http.Request, path string) template.URL {
	webcalBaseURL := webcalBaseURLFromRequest(r)
	webcalBase, err := url.Parse(webcalBaseURL)
	if err != nil || webcalBase.Scheme != "webcal" || webcalBase.Host == "" || webcalBase.User != nil {
		// Fall back to the normal base URL rather than emitting a broken link.
		fallbackBase, err := url.Parse(baseURLFromRequest(r))
		if err != nil {
			return ""
		}
		fallbackBase.Path = path
		fallbackBase.RawQuery = ""
		fallbackBase.Fragment = ""

		//nolint:gosec // Constructed from validated/derived base URL and internal path.
		return template.URL(fallbackBase.String())
	}

	webcalBase.Path = path
	webcalBase.RawQuery = ""
	webcalBase.Fragment = ""

	//nolint:gosec // Constructed from validated scheme+host and internal path.
	return template.URL(webcalBase.String())
}

func outlookSubscriptionURL(r *http.Request, path string, calendarName string) template.URL {
	base, err := url.Parse(baseURLFromRequest(r))
	if err != nil || base.Host == "" || base.User != nil {
		return ""
	}

	base.Scheme = schemeHTTPS
	base.Path = path
	base.RawQuery = ""
	base.Fragment = ""

	// Use the addfromweb endpoint to trigger a calendar subscription rather than
	// event creation. The deeplink/compose?rru=addsubscription format is no longer
	// reliable and may open the event composer instead.
	outlookURL, err := url.Parse("https://outlook.office.com/calendar/0/addfromweb")
	if err != nil {
		return ""
	}

	query := outlookURL.Query()
	query.Set("url", base.String())
	query.Set("name", calendarName)
	outlookURL.RawQuery = query.Encode()

	//nolint:gosec // Constructed from trusted URL parts and encoded query values.
	return template.URL(outlookURL.String())
}

func googleSubscriptionURL(r *http.Request, path string) template.URL {
	// Google Calendar's render?cid= endpoint accepts a webcal:// or https:// URL and
	// opens the "Add calendar" dialog with it prefilled. The /r/settings/addbyurl
	// page does not support prefilling via query parameter.
	webcalURL := webcalSubscriptionURL(r, path)
	if webcalURL == "" {
		return ""
	}

	googleURL, err := url.Parse("https://calendar.google.com/calendar/render")
	if err != nil {
		return ""
	}

	query := googleURL.Query()
	query.Set("cid", string(webcalURL))
	googleURL.RawQuery = query.Encode()

	//nolint:gosec // Constructed from trusted URL parts and encoded query values.
	return template.URL(googleURL.String())
}

func (h *Handler) handleCalendarICS(w http.ResponseWriter, r *http.Request) {
	// Get token from URL.
	token := chi.URLParam(r, "token")
	if token == "" {
		http.Error(w, "Token required", http.StatusBadRequest)
		return
	}

	opts := h.buildSupportCalendarOptions()

	// Generate ICS content using new calendar library.
	icsContent, err := calendar.GenerateICalForTokenWithOptions(
		r.Context(),
		h.db,
		token,
		defaultCalendarLookaheadDays,
		opts,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	// Record that this subscription was used for the rota calendar.
	_ = h.db.TouchRotaSubscription(r.Context(), token)

	// Set headers for calendar download.
	w.Header().Set("Content-Type", "text/calendar")
	w.Header().Set("Content-Disposition", "attachment; filename=\"support-rota.ics\"")
	w.Header().Set("Cache-Control", "no-cache")

	// Write ICS content.
	_, _ = w.Write([]byte(icsContent)) // #nosec G705 -- ICS content is generated server-side and served as text/calendar.
}

// buildSupportCalendarOptions assembles the SupportCalendarOptions from
// the operator's environment variables and the wired-in materialiser
// and holiday lookup. The same options are reused by the team-calendar
// handler so behavior is consistent.
func (h *Handler) buildSupportCalendarOptions() calendar.SupportCalendarOptions {
	return calendar.SupportCalendarOptions{
		SupportDayLinks:                   calendar.ParseMeetingLinks(os.Getenv("SUPPORT_DAY_LINKS")),
		WithAlarm:                         true,
		ShuffleSeed:                       os.Getenv("SUPPORT_DAY_SHUFFLE_SEED"),
		WFHMaterialiser:                   NewWFHMaterialiser(h.wfhService),
		WFHAssigner:                       NewWFHAssigner(h.wfhService),
		WFHCopresenceWriter:               NewWFHCopresenceWriter(h.wfhService),
		HolidayLookup:                     h.holidayLookup,
		SupportAssignmentTemplateTextPath: os.Getenv("SUPPORT_ASSIGNMENT_TEMPLATE_TEXT_PATH"),
		SupportAssignmentTemplateHTMLPath: os.Getenv("SUPPORT_ASSIGNMENT_TEMPLATE_HTML_PATH"),
		LeaveTemplateTextPath:             os.Getenv("LEAVE_TEMPLATE_TEXT_PATH"),
		LeaveTemplateHTMLPath:             os.Getenv("LEAVE_TEMPLATE_HTML_PATH"),
		HolidayTemplateTextPath:           os.Getenv("HOLIDAY_TEMPLATE_TEXT_PATH"),
		HolidayTemplateHTMLPath:           os.Getenv("HOLIDAY_TEMPLATE_HTML_PATH"),
	}
}

// buildMeetingsOptions assembles the MeetingsOptions from the
// operator's environment variables. Used by both the .ics feed
// handler and the per-day HTML view.
func (h *Handler) buildMeetingsOptions() calendar.MeetingsOptions {
	return calendar.MeetingsOptions{
		Timezone:         os.Getenv("MEETINGS_TIMEZONE"),
		TeamsURL:         os.Getenv("MEETINGS_TEAMS_URL"),
		Links:            calendar.ParseMeetingLinks(os.Getenv("MEETINGS_LINKS")),
		MorningLinks:     calendar.ParseMeetingLinks(os.Getenv("MEETINGS_LINKS_MORNING")),
		ProjectLinks:     calendar.ParseMeetingLinks(os.Getenv("MEETINGS_LINKS_PROJECT")),
		TemplateTextPath: os.Getenv("MEETINGS_TEMPLATE_TEXT_PATH"),
		TemplateHTMLPath: os.Getenv("MEETINGS_TEMPLATE_HTML_PATH"),
	}
}

func (h *Handler) handleMeetingsCalendarICS(w http.ResponseWriter, r *http.Request) {
	// Get token from URL.
	token := chi.URLParam(r, "token")
	if token == "" {
		http.Error(w, "Token required", http.StatusBadRequest)
		return
	}

	icsContent, err := calendar.GenerateMeetingsICalForToken(
		r.Context(),
		h.db,
		token,
		defaultCalendarLookaheadDays,
		h.buildMeetingsOptions(),
		h.isBusinessDay,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	// Record that this subscription was used for the meetings calendar.
	_ = h.db.TouchMeetingsSubscription(r.Context(), token)

	// Set headers for calendar download.
	w.Header().Set("Content-Type", "text/calendar")
	w.Header().Set("Content-Disposition", "attachment; filename=\"support-meetings.ics\"")
	w.Header().Set("Cache-Control", "no-cache")

	_, _ = w.Write([]byte(icsContent)) // #nosec G705 -- ICS content is generated server-side and served as text/calendar.
}

// handleMeetingsDayHTML renders a single date's meetings as an HTML
// page. The token is the authorization (must exist in
// calendar_subscriptions). Linked from the dashboard's schedule
// matrix date headers.
func (h *Handler) handleMeetingsDayHTML(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := chi.URLParam(r, "token")
	dateStr := chi.URLParam(r, "date")

	if token == "" {
		http.Error(w, "Token required", http.StatusBadRequest)
		return
	}
	if _, err := time.Parse("2006-01-02", dateStr); err != nil {
		http.Error(w, "Invalid date, expected YYYY-MM-DD", http.StatusBadRequest)
		return
	}

	// Touch the meetings subscription so the per-token "last used"
	// timestamp stays current.
	if _, err := h.db.GetMemberByToken(ctx, token); err != nil {
		http.Error(w, "Invalid token", http.StatusNotFound)
		return
	}

	day, err := calendar.GenerateMeetingsForDate(
		ctx,
		h.db,
		dateStr,
		h.buildMeetingsOptions(),
		h.isBusinessDay,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := map[string]any{
		"Template": "calendar_meetings_day",
		"Token":    token,
		"Date":     day.Date,
		"Meetings": day.Meetings,
	}
	if user, ok := auth.GetUserFromContext(ctx); ok {
		data["User"] = user
		data["IsAdmin"] = auth.IsAdminSession(user)
	}

	if err := h.tmpl.ExecuteTemplate(w, "calendar_meetings_day.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) handleTeamCalendarICS(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if token == "" {
		http.Error(w, "Token required", http.StatusBadRequest)
		return
	}

	icsContent, err := calendar.GenerateOthersICalForTokenWithOptions(
		r.Context(),
		h.db,
		token,
		defaultCalendarLookaheadDays,
		h.buildSupportCalendarOptions(),
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	_ = h.db.TouchRotaSubscription(r.Context(), token)

	w.Header().Set("Content-Type", "text/calendar")
	w.Header().Set("Content-Disposition", "attachment; filename=\"support-rota-others.ics\"")
	w.Header().Set("Cache-Control", "no-cache")

	_, _ = w.Write([]byte(icsContent)) // #nosec G705 -- ICS content is generated server-side and served as text/calendar.
}

func baseURLFromRequest(r *http.Request) string {
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}

	scheme := schemeFromRequestTLS(r)

	// Reverse proxies typically forward the original scheme.
	if forwarded := strings.TrimSpace(r.Header.Get("Forwarded")); forwarded != "" {
		forwardedProto, forwardedHost := parseForwardedHeader(forwarded)
		if forwardedProto != "" {
			scheme = forwardedProto
		}
		if forwardedHost != "" {
			host = forwardedHost
		}
	} else if xfProto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); xfProto != "" {
		// Example: X-Forwarded-Proto: https
		scheme, _, _ = strings.Cut(xfProto, ",")
		scheme = strings.TrimSpace(scheme)
	}

	if host == "" {
		// Best-effort fallback; should be rare.
		host = "localhost"
	}

	return scheme + "://" + host
}

func webcalBaseURLFromRequest(r *http.Request) string {
	baseURL := baseURLFromRequest(r)
	u, err := url.Parse(baseURL)
	if err != nil {
		return "webcal://" + strings.TrimPrefix(strings.TrimPrefix(baseURL, "https://"), "http://")
	}
	if u.Host == "" {
		return "webcal://" + strings.TrimPrefix(strings.TrimPrefix(baseURL, "https://"), "http://")
	}
	return "webcal://" + u.Host
}

func schemeFromRequestTLS(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func parseForwardedHeader(headerValue string) (proto string, host string) {
	const maxSplitParts = 2

	// Example: Forwarded: for=1.2.3.4;proto=https;host=example.com
	first, _, _ := strings.Cut(headerValue, ",")
	for part := range strings.SplitSeq(first, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", maxSplitParts)
		if len(kv) != maxSplitParts {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(kv[0]))
		value := strings.Trim(strings.TrimSpace(kv[1]), "\"")
		switch key {
		case "proto":
			if value != "" {
				proto = value
			}
		case "host":
			if value != "" {
				host = value
			}
		}
	}

	return proto, host
}

// defaultStaleSubscriptionDays is the default number of days of inactivity before a
// subscription is considered stale. Admins can override via the cleanup form.
const defaultStaleSubscriptionDays = 90

func (h *Handler) renderCalendarSubscriptionsPage(w http.ResponseWriter, r *http.Request, staleDays int, deleted int64) {
	ctx := r.Context()

	data := map[string]any{
		"Template":         "calendar_subscriptions",
		"DefaultStaleDays": staleDays,
	}

	if deleted > 0 {
		data["Deleted"] = deleted
	}

	if user, ok := auth.GetUserFromContext(ctx); ok {
		data["User"] = user
		data["IsAdmin"] = auth.IsAdminSession(user)
	}

	subs, err := h.db.GetAllSubscriptions(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data["Subscriptions"] = subs

	if err := h.tmpl.ExecuteTemplate(w, "calendar_subscriptions.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) handleCalendarSubscriptions(w http.ResponseWriter, r *http.Request) {
	h.renderCalendarSubscriptionsPage(w, r, defaultStaleSubscriptionDays, 0)
}

func (h *Handler) handleCalendarSubscriptionsCleanup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxCalendarFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	days := defaultStaleSubscriptionDays
	if v := strings.TrimSpace(r.PostForm.Get("days")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			days = n
		}
	}

	cutoff := time.Now().AddDate(0, 0, -days)
	deleted, err := h.db.DeleteStaleSubscriptions(ctx, cutoff)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.renderCalendarSubscriptionsPage(w, r, days, deleted)
}
