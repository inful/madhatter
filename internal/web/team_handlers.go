package web

import (
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/auth"
)

func (h *Handler) handleTeam(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := map[string]any{
		"Template": "team",
	}

	// Add user info to data.
	if user, ok := auth.GetUserFromContext(ctx); ok {
		data["User"] = user
		data["IsAdmin"] = user.IsAdmin.Valid && user.IsAdmin.Int64 == 1
	}

	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		name := r.FormValue("name")
		email := r.FormValue("email")

		_, err := h.db.AddTeamMember(ctx, name, email)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Handle team change - update schedule.
		if err := h.maintenance.HandleTeamChange(ctx); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, "/team", http.StatusSeeOther)
		return
	}

	members, err := h.db.GetActiveTeamMembers(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data["Members"] = members

	// Build subscription activity map: member ID → {RotaActive, MeetingsActive}.
	// A subscription is "active" if it was used in the last 7 days.
	const activeSubscriptionDays = 7
	since := time.Now().AddDate(0, 0, -activeSubscriptionDays)
	activity, err := h.db.GetSubscriptionActivityByMember(ctx, since)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data["SubscriptionActivity"] = activity

	if err := h.tmpl.ExecuteTemplate(w, "team.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// validateTeamMemberInput validates name and email inputs.
func validateTeamMemberInput(name, email string) error {
	if name == "" {
		return errors.New("name cannot be empty")
	}
	if email == "" {
		return errors.New("email cannot be empty")
	}
	if len(name) > maxStringLength {
		return errors.New("name is too long (max 255 characters)")
	}
	if len(email) > maxStringLength {
		return errors.New("email is too long (max 255 characters)")
	}
	// Validate email format.
	if _, err := mail.ParseAddress(email); err != nil {
		return errors.New("invalid email format")
	}
	return nil
}

func (h *Handler) handleTeamMemberEdit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	memberID := chi.URLParam(r, "id")

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	email := strings.TrimSpace(r.FormValue("email"))

	// Validate input at handler level.
	if err := validateTeamMemberInput(name, email); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.db.UpdateTeamMember(ctx, memberID, name, email); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/team", http.StatusSeeOther)
}

func (h *Handler) handleTeamMemberDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	memberID := chi.URLParam(r, "id")

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := h.db.DeleteTeamMember(ctx, memberID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Handle team change - update schedule.
	if err := h.maintenance.HandleTeamChange(ctx); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/team", http.StatusSeeOther)
}
