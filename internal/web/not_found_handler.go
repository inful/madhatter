package web

import (
	"net/http"

	"github.com/inful/madhatter/internal/auth"
)

// handleNotFound renders the styled 404 page. Wired via
// router.NotFound so any unmatched path lands here instead of chi's
// default "404 page not found" text response. The 404 status is set
// explicitly so curl / search-engine crawlers see the right code.
func (h *Handler) handleNotFound(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := map[string]any{
		"Template": "not_found",
	}
	if user, ok := auth.GetUserFromContext(ctx); ok {
		data["User"] = user
		data["IsAdmin"] = auth.IsAdminSession(user)
	}
	w.WriteHeader(http.StatusNotFound)
	if err := h.tmpl.ExecuteTemplate(w, "not_found.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
