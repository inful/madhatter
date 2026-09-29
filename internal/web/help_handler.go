package web

import (
	"net/http"

	"github.com/inful/madhatter/internal/auth"
)

// handleHelp renders the in-app user guide. The page is intentionally
// minimal: no env-var tables, no configuration, no operational
// detail — the audience is end-users of the system, not operators.
// Configuration surfaces live in the README and the docs/ folder;
// the /help page answers "how do I use this?" rather than "how is
// this configured?".
func (h *Handler) handleHelp(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := map[string]any{
		"Template": "help",
	}

	if user, ok := auth.GetUserFromContext(ctx); ok {
		data["User"] = user
		data["IsAdmin"] = auth.IsAdminSession(user)
	}

	if err := h.tmpl.ExecuteTemplate(w, "help.html", data); err != nil {
		httpError(w, r, http.StatusInternalServerError, "Internal server error.", err)
	}
}
