package web

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/auth"
)

func (h *Handler) registerRoutes() {
	// Auth routes (no authentication required) - only if auth is configured.
	if h.authManager != nil {
		// In development mode, skip registering the production login handler.
		// The development handler will be registered later in registerDevelopmentRoutes().
		if !h.development {
			h.router.HandleFunc("/login", h.authManager.HandleLoginView)
		}
		h.router.HandleFunc("/auth/login/{provider}", h.authManager.HandleLogin)
		h.router.HandleFunc("/auth/callback", h.authManager.HandleCallback)
		h.router.HandleFunc("/auth/logout", h.authManager.HandleLogout)
	} else {
		// Provide a helpful page instead of a 404 when auth is disabled.
		h.router.HandleFunc("/login", h.handleLoginUnavailable)
	}

	// Public routes (no authentication required, but user info loaded if available).
	if h.authMiddleware != nil {
		// When auth is configured, hide the schedule until the user has authenticated.
		h.router.Group(func(r chi.Router) {
			r.Use(h.safeRequireAuth)
			r.HandleFunc("/", h.handleDashboard)
		})
	} else {
		h.router.Group(func(r chi.Router) {
			r.Use(h.safeAuthMiddleware)
			r.HandleFunc("/", h.handleDashboard)
		})
	}

	h.router.HandleFunc("/calendar/{token}/ics", h.handleCalendarICS)
	h.router.HandleFunc("/calendar/{token}/team.ics", h.handleTeamCalendarICS)
	h.router.HandleFunc("/calendar/{token}/meetings.ics", h.handleMeetingsCalendarICS)

	// Protected routes (require authentication).
	h.router.Group(func(r chi.Router) {
		r.Use(h.safeRequireAuth)

		r.HandleFunc("/leave/report", h.handleLeaveReport)
		r.HandleFunc("/calendar", h.handleCalendar)
		r.HandleFunc("/swaps", h.handleSwaps)
		r.Post("/swaps/{id}/cancel", h.handleSwapCancel)
		r.Post("/swaps/{id}/accept", h.handleSwapAccept)
		r.Post("/swaps/{id}/reject", h.handleSwapReject)

		r.HandleFunc("/wfh", h.handleWFHList)
		r.HandleFunc("/wfh/request", h.handleWFHRequest)
		r.Post("/wfh/{id}/cancel", h.handleWFHCancel)
	})

	// Admin routes (require authentication and admin privileges).
	h.router.Group(func(r chi.Router) {
		r.Use(h.safeRequireAuth)
		r.Use(h.safeRequireAdmin)

		r.HandleFunc("/team", h.handleTeam)
		r.HandleFunc("/team/{id}/edit", h.handleTeamMemberEdit)
		r.HandleFunc("/team/{id}/delete", h.handleTeamMemberDelete)
		r.HandleFunc("/team/users/{id}/admin", h.handleUserAdminUpdate)
		r.HandleFunc("/leave/manage", h.handleLeaveManagement)
		r.HandleFunc("/leave/{id}/edit", h.handleLeaveEdit)
		r.HandleFunc("/leave/{id}/delete", h.handleLeaveDelete)
		r.HandleFunc("/schedule/generate", h.handleScheduleGenerate)
		r.HandleFunc("/admin/database/backup", h.handleDatabaseBackup)
		r.HandleFunc("/admin/database/restore", h.handleDatabaseRestore)
		r.HandleFunc("/calendar/subscriptions", h.handleCalendarSubscriptions)
		r.HandleFunc("/calendar/subscriptions/cleanup", h.handleCalendarSubscriptionsCleanup)
		r.Post("/swaps/{id}/delete", h.handleSwapAdminDelete)
		r.Get("/admin/wfh", h.handleWFHAdminPage)
		r.Post("/admin/wfh/{id}/withdraw", h.handleWFHAdminWithdraw)
		r.Post("/admin/wfh/settle", h.handleWFHAdminSettle)
	})
}

// registerDevelopmentRoutes adds development-specific routes.
func (h *Handler) registerDevelopmentRoutes() {
	if h.authManager == nil {
		return
	}

	// Check if this is a fake provider.
	provider, err := h.authManager.GetProvider("fake")
	if err != nil || provider == nil {
		return
	}

	// Override the login view to show development mode message.
	h.router.HandleFunc("/login", h.handleDevelopmentLogin)
}

func (h *Handler) handleDevelopmentLogin(w http.ResponseWriter, r *http.Request) {
	// Check if already logged in.
	if h.isUserLoggedIn(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	// Show development mode login page.
	h.renderDevelopmentLogin(w)
}

func (h *Handler) isUserLoggedIn(r *http.Request) bool {
	token, err := h.authManager.GetSessionManager().GetSessionCookie(r)
	if err != nil {
		return false
	}

	_, err = h.authManager.GetSessionManager().ValidateSession(r.Context(), token)
	return err == nil
}

func (h *Handler) renderDevelopmentLogin(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html")
	// Use shared HTML from auth package to eliminate duplication.
	_, _ = w.Write([]byte(auth.GetDevelopmentLoginHTML()))
}

// Router returns the underlying chi router.
func (h *Handler) Router() *chi.Mux {
	return h.router
}
