package web

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/ratelimit"
)

func (h *Handler) registerRoutes() {
	// Auth routes (no authentication required) - only if auth is configured.
	if h.authManager != nil {
		// In development mode, skip registering the production login handler.
		// The development handler will be registered later in registerDevelopmentRoutes().
		if !h.development {
			h.router.HandleFunc("/login", h.authManager.HandleLoginView)
		}
		// The OAuth initiation route is the one an attacker would
		// brute-force against. Apply the per-IP rate limit so a
		// single source can't drive the whole login flow at full
		// TCP speed. The callback and logout routes are not limited
		// because they require a session/state machine to do damage.
		if h.authRateLimiter != nil {
			h.router.With(ratelimit.MiddlewareFactory(h.authRateLimiter)).
				HandleFunc("/auth/login/{provider}", h.authManager.HandleLogin)
		} else {
			h.router.HandleFunc("/auth/login/{provider}", h.authManager.HandleLogin)
		}
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
		r.HandleFunc("/leave/report-sick", h.handleLeaveReportSick)
		r.HandleFunc("/leave/manage", h.handleLeaveManagement)
		r.Post("/leave/{id}/edit", h.handleLeaveEdit)
		r.Post("/leave/{id}/delete", h.handleLeaveDelete)
		r.HandleFunc("/calendar", h.handleCalendar)
		r.HandleFunc("/swaps", h.handleSwaps)
		r.Post("/swaps/{id}/cancel", h.handleSwapCancel)
		r.Post("/swaps/{id}/accept", h.handleSwapAccept)
		r.Post("/swaps/{id}/reject", h.handleSwapReject)

		r.HandleFunc("/wfh", h.handleWFHList)
		r.HandleFunc("/wfh/request", h.handleWFHRequest)
		r.Post("/wfh/report-today", h.handleWFHReportToday)
		r.Post("/wfh/{id}/cancel", h.handleWFHCancel)
		r.Post("/wfh/{id}/withdraw", h.handleWFHSelfWithdraw)
		r.Get("/wfh/{id}/swap", h.handleWFHSwapForm)
		r.Post("/wfh/{id}/swap", h.handleWFHSwapCreate)
		r.Get("/wfh/swap/inbox", h.handleWFHSwapInbox)
		r.Post("/wfh/swap/{swapId}/accept", h.handleWFHSwapAccept)
		r.Post("/wfh/swap/{swapId}/reject", h.handleWFHSwapReject)
		r.Post("/wfh/swap/{swapId}/cancel", h.handleWFHSwapCancel)
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
		// Admin override: mark a member as WFH today even though
		// they didn't request it. The "unmark" action reuses the
		// existing withdraw path but is guarded so it can only act
		// on rows with is_admin_marked=1.
		r.Get("/admin/wfh/mark", h.handleAdminMarkWFHPage)
		r.Post("/admin/wfh/mark", h.handleAdminMarkWFH)
		r.Post("/admin/wfh/{id}/unmark", h.handleAdminMarkWFHUnmark)
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
