package web

import (
	"database/sql"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database/sqlc"
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
	users, err := h.db.GetQueries().ListActiveUsers(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	adminCount, err := h.db.GetQueries().CountAdmins(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data["Members"] = members
	data["Users"] = users
	data["AdminCount"] = adminCount

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

func (h *Handler) handleUserAdminUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := chi.URLParam(r, "id")

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	makeAdmin := r.FormValue("is_admin") == "1"
	user, err := h.db.GetQueries().GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	isAdmin := user.IsAdmin.Valid && user.IsAdmin.Int64 == 1
	if isAdmin && !makeAdmin {
		adminCount, countErr := h.db.GetQueries().CountAdmins(ctx)
		if countErr != nil {
			http.Error(w, countErr.Error(), http.StatusInternalServerError)
			return
		}
		if adminCount <= 1 {
			http.Error(w, "cannot remove the last admin", http.StatusBadRequest)
			return
		}
	}

	if updateErr := h.db.GetQueries().UpdateUser(ctx, sqlc.UpdateUserParams{
		Name:     user.Name,
		IsAdmin:  sql.NullInt64{Int64: adminInt(makeAdmin), Valid: true},
		IsActive: user.IsActive,
		ID:       user.ID,
	}); updateErr != nil {
		http.Error(w, updateErr.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/team", http.StatusSeeOther)
}

func adminInt(isAdmin bool) int64 {
	if isAdmin {
		return 1
	}
	return 0
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
