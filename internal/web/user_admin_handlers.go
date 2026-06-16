package web

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database/sqlc"
)

// handleUserApprove activates a pending user. Admin-only.
func (h *Handler) handleUserApprove(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	userID := chi.URLParam(r, "id")

	// Approve is a state change, not a deletion. The user already
	// has a row; ApproveUser flips is_active to 1 and clears
	// deactivated_at. We don't touch sessions — any session the
	// pending user managed to acquire (e.g. a stale cookie from
	// before the rule change) is now valid because ValidateSession
	// will accept the user.
	_, err := h.db.GetQueries().ApproveUser(ctx, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "user not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/team", http.StatusSeeOther)
}

// handleUserDeny rejects a pending user. The user row, their
// sessions, and their OAuth tokens are deleted; their team member
// is deactivated (preserved for audit but hidden from the active
// schedule). Admin-only.
func (h *Handler) handleUserDeny(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	userID := chi.URLParam(r, "id")

	// Look up the user first so we know their email (for the team
	// member deactivation). If the user is already gone (e.g. a
	// double-click on deny) we just redirect.
	user, err := h.db.GetQueries().GetUserByID(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		http.Redirect(w, r, "/team", http.StatusSeeOther)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := h.denyPendingUser(ctx, user); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/team", http.StatusSeeOther)
}

// denyPendingUser removes the user row, their sessions, and their
// OAuth tokens in a single SQLite transaction. The team member row
// is deactivated (rather than deleted) so the audit trail of "this
// person signed in but was denied" survives.
func (h *Handler) denyPendingUser(ctx context.Context, user sqlc.User) error {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	q := h.db.GetQueries().WithTx(tx)
	if err := q.DeleteUserOAuthTokens(ctx, user.ID); err != nil {
		return err
	}
	if err := q.DeleteUserSessions(ctx, user.ID); err != nil {
		return err
	}
	if err := q.DeleteUser(ctx, user.ID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	// Deactivate the team member (if any) outside the transaction.
	// GetMemberByEmail is best-effort; an absent member row is not
	// an error.
	if member, err := h.db.GetMemberByEmail(ctx, user.Email); err == nil && member != nil {
		_ = h.db.GetQueries().DeactivateTeamMember(ctx, member.ID)
	}

	return nil
}

// handleUserDeactivate deactivates an active user. Their sessions
// become invalid on the next request (ValidateSession rejects
// is_active = 0). Admin-only.
func (h *Handler) handleUserDeactivate(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	userID := chi.URLParam(r, "id")

	user, err := h.db.GetQueries().GetUserByID(ctx, userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if !auth.IsAdmin(user.IsAdmin) {
		http.Error(w, "refusing to deactivate the last admin", http.StatusForbidden)
		return
	}

	if _, err := h.db.GetQueries().DeactivateUser(ctx, userID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Also deactivate the team member so they don't show up on
	// the active schedule. Best-effort.
	if member, err := h.db.GetMemberByEmail(ctx, user.Email); err == nil && member != nil {
		_ = h.db.GetQueries().DeactivateTeamMember(ctx, member.ID)
	}

	http.Redirect(w, r, "/team", http.StatusSeeOther)
}

// handleUserReactivate clears the deactivated flag on a user that
// was previously deactivated by an admin. Admin-only.
func (h *Handler) handleUserReactivate(w http.ResponseWriter, r *http.Request) {
	if !h.requireAdmin(w, r) {
		return
	}
	ctx := r.Context()
	userID := chi.URLParam(r, "id")

	if _, err := h.db.GetQueries().ReactivateUser(ctx, userID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Reactivate the team member too so they reappear on the
	// schedule. Best-effort.
	user, err := h.db.GetQueries().GetUserByID(ctx, userID)
	if err == nil {
		if member, err := h.db.GetMemberByEmail(ctx, user.Email); err == nil && member != nil {
			_ = h.db.GetQueries().ActivateTeamMember(ctx, member.ID)
		}
	}

	http.Redirect(w, r, "/team", http.StatusSeeOther)
}

// requireAdmin is a small helper that returns true iff the request
// was made by an active admin. The redirect target is /login so
// expired sessions land in a sensible place. Used by every
// approval-flow handler so the admin-gating is in one place.
func (h *Handler) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	user, ok := auth.GetUserFromContext(r.Context())
	if !ok || !auth.IsAdminSession(user) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return false
	}
	return true
}
