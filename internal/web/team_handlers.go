package web

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
	"github.com/inful/madhatter/internal/database/sqlc"
)

const maxTeamFormBytes = 1 << 20

type teamUserView struct {
	ID          string
	Name        string
	Email       string
	IsAdmin     bool
	IsActive    bool
	Deactivated bool
	CreatedAt   string
}

func (h *Handler) handleTeamPost(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	r.Body = http.MaxBytesReader(w, r.Body, maxTeamFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.PostForm.Get("name"))
	email := strings.TrimSpace(r.PostForm.Get("email"))

	// Validate at handler level so an admin typing an env-var
	// name (or any other non-address) into the email field gets
	// immediate feedback, not a 30-minute outbox retry loop.
	if err := validateTeamMemberInput(name, email); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	_, err := h.db.AddTeamMember(ctx, name, email)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := h.maintenance.HandleTeamChange(ctx); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/team", http.StatusSeeOther)
}

//nolint:cyclop // Team page handler orchestrates reads/sync/render branches.
func (h *Handler) handleTeam(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := map[string]any{
		"Template": "team",
	}

	if user, ok := auth.GetUserFromContext(ctx); ok {
		data["User"] = user
		data["IsAdmin"] = auth.IsAdminSession(user)
	}

	if r.Method == http.MethodPost {
		h.handleTeamPost(w, r)
		return
	}

	members, err := h.db.GetActiveTeamMembers(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if h.development {
		if syncErr := h.syncDevelopmentUsersWithTeamMembers(ctx, members); syncErr != nil {
			http.Error(w, syncErr.Error(), http.StatusInternalServerError)
			return
		}
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
	pendingUsers, err := h.db.GetQueries().ListPendingUsers(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	deactivatedUsers, err := h.db.GetQueries().ListDeactivatedUsers(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data["Members"] = members
	data["Users"] = buildUserViews(users)
	data["PendingUsers"] = buildUserViews(pendingUsers)
	data["DeactivatedUsers"] = buildUserViews(deactivatedUsers)
	data["AdminCount"] = adminCount
	data["PendingCount"] = len(pendingUsers)

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

// buildUserViews converts a slice of sqlc.User rows into the
// template-facing view struct. The "IsActive" and "Deactivated"
// flags are derived from the row's is_active and deactivated_at
// columns. "CreatedAt" is a render-ready timestamp so the
// template can show "signed in 3 days ago" without doing the
// formatting itself.
func buildUserViews(rows []sqlc.User) []teamUserView {
	views := make([]teamUserView, 0, len(rows))
	for i := range rows {
		u := rows[i]
		views = append(views, teamUserView{
			ID:          u.ID,
			Name:        u.Name,
			Email:       u.Email,
			IsAdmin:     auth.IsAdmin(u.IsAdmin),
			IsActive:    u.IsActive.Valid && u.IsActive.Int64 == 1,
			Deactivated: u.DeactivatedAt.Valid,
			CreatedAt:   formatUserCreatedAt(u.CreatedAt),
		})
	}
	return views
}

// formatUserCreatedAt renders a user.CreatedAt timestamp for the
// template, returning "" if the value is absent. The format is
// "2006-01-02 15:04 UTC" — enough granularity for the team page
// to show "signed in N days ago".
func formatUserCreatedAt(t sql.NullTime) string {
	if !t.Valid {
		return ""
	}
	return t.Time.UTC().Format("2006-01-02 15:04 MST")
}

func (h *Handler) syncDevelopmentUsersWithTeamMembers(ctx context.Context, members []database.TeamMember) error {
	for _, member := range members {
		_, err := h.db.GetQueries().GetUserByEmail(ctx, member.Email)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		_, createErr := h.db.GetQueries().CreateActiveUser(ctx, sqlc.CreateActiveUserParams{
			ID:         uuid.New().String(),
			Email:      member.Email,
			Name:       member.Name,
			Provider:   "fake",
			ProviderID: member.Email,
		})
		if createErr != nil {
			return createErr
		}
	}

	return nil
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

	r.Body = http.MaxBytesReader(w, r.Body, maxTeamFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(r.PostForm.Get("name"))
	email := strings.TrimSpace(r.PostForm.Get("email"))

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

func (h *Handler) handleTeamMemberPermanentWFHUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	memberID := chi.URLParam(r, "id")

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxTeamFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	days := database.RecurringWFHDays{
		Monday:    parseCheckboxBool(r.PostForm.Get("recurring_wfh_monday")),
		Tuesday:   parseCheckboxBool(r.PostForm.Get("recurring_wfh_tuesday")),
		Wednesday: parseCheckboxBool(r.PostForm.Get("recurring_wfh_wednesday")),
		Thursday:  parseCheckboxBool(r.PostForm.Get("recurring_wfh_thursday")),
		Friday:    parseCheckboxBool(r.PostForm.Get("recurring_wfh_friday")),
	}

	_, err := h.db.GetMemberByID(ctx, memberID)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "team member not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := h.db.SetTeamMemberRecurringWFHDays(ctx, memberID, days); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/team", http.StatusSeeOther)
}

// handleTeamMemberExemptUpdate toggles the is_exempt_from_assignment
// flag. The picker excludes exempt members from its candidate pool
// (see service.go isExemptFromAssignment check), so an admin who
// never wants this member picked as Assigned WFH can flip the
// checkbox once. POST /team/{id}/exempt. Body:
// is_exempt_from_assignment=1 when checked.
//
// Step 17 of plans/assigned-wfh-plan.md. The DB column and the
// SetTeamMemberExemptFromAssignment method shipped in Phase 1
// (migration 26); this handler is the form-side wiring that
// Phase 4 deferred.
func (h *Handler) handleTeamMemberExemptUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	memberID := chi.URLParam(r, "id")

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxTeamFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	_, err := h.db.GetMemberByID(ctx, memberID)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "team member not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	exempt := parseCheckboxBool(r.PostForm.Get("is_exempt_from_assignment"))
	if err := h.db.SetTeamMemberExemptFromAssignment(ctx, memberID, exempt); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/team", http.StatusSeeOther)
}

func parseCheckboxBool(v string) bool {
	return v == "1" || v == "on" || strings.EqualFold(v, "true")
}

// guardAdminDemotion returns a non-zero HTTP status and error when demoting isCurrentlyAdmin
// to non-admin would violate the last-admin invariant.
func (h *Handler) guardAdminDemotion(ctx context.Context, isCurrentlyAdmin, makeAdmin bool) (int, error) {
	if !isCurrentlyAdmin || makeAdmin {
		return 0, nil
	}

	adminCount, err := h.db.GetQueries().CountAdmins(ctx)
	if err != nil {
		return http.StatusInternalServerError, err
	}

	if adminCount <= 1 {
		return http.StatusBadRequest, errors.New("cannot remove the last admin")
	}

	return 0, nil
}

func (h *Handler) handleUserAdminUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := chi.URLParam(r, "id")

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxTeamFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	makeAdmin := r.PostForm.Get("is_admin") == "1"
	user, err := h.db.GetQueries().GetUserByID(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	isAdmin := auth.IsAdmin(user.IsAdmin)
	if status, guardErr := h.guardAdminDemotion(ctx, isAdmin, makeAdmin); guardErr != nil {
		http.Error(w, guardErr.Error(), status)
		return
	}

	if err = h.db.GetQueries().UpdateUser(ctx, sqlc.UpdateUserParams{
		Name:     user.Name,
		IsAdmin:  sql.NullInt64{Int64: boolToAdminInt(makeAdmin), Valid: true},
		IsActive: user.IsActive,
		ID:       user.ID,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/team", http.StatusSeeOther)
}

func boolToAdminInt(isAdmin bool) int64 {
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
