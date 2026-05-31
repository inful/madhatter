package web

import (
	"net/http"

	"github.com/inful/madhatter/internal/auth"
)

func (h *Handler) handleHelp(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := map[string]any{
		"Template": "help",
	}

	if user, ok := auth.GetUserFromContext(ctx); ok {
		data["User"] = user
		data["IsAdmin"] = auth.IsAdminSession(user)
	}

	if h.wfhService != nil {
		cfg := h.wfhService.Config()
		data["WFHConfigured"] = true
		data["WFHSettlementDays"] = cfg.SettlementDays
		data["WFHMinOnsitePercentage"] = cfg.MinOnsitePercentage
		data["WFHMinOnsiteAbsolute"] = cfg.MinOnsiteAbsolute
		data["WFHMaxDaysPerPeriod"] = cfg.MaxDaysPerPeriod
		data["WFHPeriodDays"] = cfg.PeriodDays
		data["WFHPeriodAnchor"] = cfg.PeriodAnchor
		data["WFHWithdrawalHours"] = cfg.WithdrawalHours
	} else {
		data["WFHConfigured"] = false
	}

	if err := h.tmpl.ExecuteTemplate(w, "help.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}