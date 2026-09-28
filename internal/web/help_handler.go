package web

import (
	"net/http"
	"strings"

	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/envutil"
)

// handleHelp renders the in-app help page. The page is a navigation
// aid: it explains user-facing concepts in prose and surfaces the
// runtime configuration as a compact table at the bottom of the
// admin card so operators can confirm the deployment actually
// matches what the docs say. Every block the template renders is
// gated by a per-feature "Configured" boolean so the page stays
// compact for small deployments — a deployment without a seat cap
// doesn't see seat-cap knobs, one without email notifications
// doesn't see SMTP settings.
//
// The Auth block reads the per-provider env-var presence directly
// rather than asking the AuthManager, so the help page reflects the
// env-var contract without adding an exported accessor on the
// auth package. The Notification block reads NOTIFY_EMAIL_ENABLED
// via envutil so the same env-var table the rest of the codebase
// uses is the source of truth.
func (h *Handler) handleHelp(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := map[string]any{
		"Template": "help",
	}

	if user, ok := auth.GetUserFromContext(ctx); ok {
		data["User"] = user
		data["IsAdmin"] = auth.IsAdminSession(user)
	}

	// WFH block — exposed when the WFH service is wired. The seat-cap
	// sub-block is independently gated on SeatCap > 0 so deployments
	// without a cap don't show the cap knobs.
	if h.wfhService != nil {
		cfg := h.wfhService.Config()
		data["WFHConfigured"] = true
		data["WFHSettlementDays"] = cfg.SettlementDays
		data["WFHSettlementInterval"] = cfg.SettlementInterval.String()
		data["WFHMinOnsitePercentage"] = cfg.MinOnsitePercentage
		data["WFHMinOnsiteAbsolute"] = cfg.MinOnsiteAbsolute
		data["WFHMaxDaysPerPeriod"] = cfg.MaxDaysPerPeriod
		data["WFHPeriodDays"] = cfg.PeriodDays
		data["WFHRequestHorizonDays"] = cfg.RequestHorizonDays

		data["WFHSeatCapConfigured"] = cfg.SeatCap > 0
		data["WFHSeatCap"] = cfg.SeatCap
		data["WFHAssignmentEnabled"] = cfg.AssignmentEnabled
		data["WFHCoPresenceEnabled"] = cfg.CoPresenceEnabled
		data["WFHCoPresenceHorizonDays"] = cfg.CoPresenceHorizonDays
		data["WFHCoPresenceRetentionDays"] = cfg.CoPresenceRetentionDays
		data["WFHPurgeEnabled"] = cfg.PurgeEnabled
	} else {
		data["WFHConfigured"] = false
	}

	// Notification block — exposed when email delivery is enabled.
	// SMTP host is only interesting when the channel is on, so it's
	// gated by the same flag; we don't read it for deployments that
	// ship notifications disabled.
	data["NotificationEmailEnabled"] = envutil.Bool("NOTIFY_EMAIL_ENABLED", false)
	if data["NotificationEmailEnabled"].(bool) {
		data["NotificationSMTPHost"] = strings.TrimSpace(envutil.String("NOTIFY_SMTP_HOST", ""))
		data["NotificationPublicBaseURL"] = strings.TrimSpace(envutil.String("NOTIFY_PUBLIC_BASE_URL", ""))
	}

	// Auth block — list the OAuth providers with their *CLIENT_ID set.
	// The auth manager doesn't expose a ProviderNames() method (the
	// provider map is unexported), so we read the per-provider env
	// vars directly. This is the same contract AUTH_SETUP.md and
	// the README env-var table use, so a deployment that follows the
	// docs will see itself reflected here.
	providers := []string{}
	if envutil.String("FORGEJO_CLIENT_ID", "") != "" {
		providers = append(providers, "Forgejo")
	}
	if envutil.String("GITLAB_CLIENT_ID", "") != "" {
		providers = append(providers, "GitLab")
	}
	data["AuthProviders"] = providers
	data["AuthConfigured"] = len(providers) > 0
	if envutil.String("SESSION_SECRET", "") != "" {
		data["SessionSecretConfigured"] = true
	} else {
		data["SessionSecretConfigured"] = false
	}

	// Rate-limit block — the bucket sizes are hardcoded constants in
	// the code today (env-var overrides are planned but not wired, see
	// API_AUTH_IMPLEMENTATION.md#roadmap). The block is always shown
	// because rate limiting is always on.
	data["RateLimitAuthBucket"] = 10
	data["RateLimitAuthBucketUnit"] = "request / minute per IP"
	data["RateLimitTokensBucket"] = 30
	data["RateLimitTokensBucketUnit"] = "request / minute per IP"

	if err := h.tmpl.ExecuteTemplate(w, "help.html", data); err != nil {
		httpError(w, r, http.StatusInternalServerError, "Internal server error.", err)
	}
}
