package web

import (
	"context"
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
	h.router.HandleFunc("/calendar/{token}/meetings/{date}.html", h.handleMeetingsDayHTML)
	h.router.With(h.safeAuthMiddleware).HandleFunc("/help", h.handleHelp)

	// Public one-click unsubscribe endpoints. The token in the
	// query string is the only auth — there is no session — so
	// the handler must verify the HMAC before touching state.
	// The /resume endpoint reverses an earlier unsubscribe.
	h.router.HandleFunc("/unsubscribe", h.handleUnsubscribe)
	h.router.Post("/unsubscribe/resume", h.handleUnsubscribeResume)

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
		r.Post("/wfh/{id}/withdraw", h.handleWFHSelfWithdraw)
	})

	// Admin routes (require authentication and admin privileges).
	h.router.Group(func(r chi.Router) {
		r.Use(h.safeRequireAuth)
		r.Use(h.safeRequireAdmin)

		r.HandleFunc("/team", h.handleTeam)
		r.HandleFunc("/team/{id}/edit", h.handleTeamMemberEdit)
		r.HandleFunc("/team/{id}/recurring-wfh", h.handleTeamMemberPermanentWFHUpdate)
		r.HandleFunc("/team/{id}/permanent-wfh", h.handleTeamMemberPermanentWFHUpdate)
		r.HandleFunc("/team/{id}/delete", h.handleTeamMemberDelete)
		r.HandleFunc("/team/users/{id}/admin", h.handleUserAdminUpdate)
		r.HandleFunc("/team/users/{id}/approve", h.handleUserApprove)
		r.HandleFunc("/team/users/{id}/deny", h.handleUserDeny)
		r.HandleFunc("/team/users/{id}/deactivate", h.handleUserDeactivate)
		r.HandleFunc("/team/users/{id}/reactivate", h.handleUserReactivate)
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
		r.Get("/admin/wfh/purge", h.handleWFHPurge)
		r.Post("/admin/wfh/purge", h.handleWFHPurge)
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
	h.renderDevelopmentLogin(w, r.Context())
}

func (h *Handler) isUserLoggedIn(r *http.Request) bool {
	token, err := h.authManager.GetSessionManager().GetSessionCookie(r)
	if err != nil {
		return false
	}

	_, err = h.authManager.GetSessionManager().ValidateSession(r.Context(), token)
	return err == nil
}

func (h *Handler) renderDevelopmentLogin(w http.ResponseWriter, ctx context.Context) {
	w.Header().Set("Content-Type", "text/html")
	users, err := h.authManager.GetDevelopmentUsers(ctx)
	if err != nil {
		users = nil
	}
	// Use shared HTML from auth package to eliminate duplication.
	_, _ = w.Write([]byte(auth.GetDevelopmentLoginHTMLWithUsers(users)))
}

// Router returns the underlying chi router.
func (h *Handler) Router() *chi.Mux {
	return h.router
}
