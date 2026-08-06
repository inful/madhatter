package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
)

const maxLeaveFormBytes = 1 << 20

// enrichedLeave is the per-row payload the leave_management template
// consumes: a LeaveRecord plus a denormalised member name.
type enrichedLeave struct {
	Leave      database.LeaveRecord
	MemberName string
}

func (h *Handler) handleLeaveReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := map[string]any{
		"Template": "leave_report",
	}

	// Add user info to data.
	if user, ok := auth.GetUserFromContext(ctx); ok {
		data["User"] = user
		data["IsAdmin"] = auth.IsAdminSession(user)
		// Non-admins can only report leave for themselves; surface
		// the resolved self member id so the form can render a
		// disabled "you" field instead of the team picker.
		if !auth.IsAdminSession(user) {
			data["SelfMemberID"] = h.resolveMemberID(ctx, user.Email)
		}
	}

	if r.Method == http.MethodPost {
		h.handleLeaveReportPost(w, r, data)
		return
	}

	h.renderLeaveReportForm(w, r, data, "", "", "", "")
}

func (h *Handler) handleLeaveReportPost(w http.ResponseWriter, r *http.Request, data map[string]any) {
	ctx := r.Context()
	r.Body = http.MaxBytesReader(w, r.Body, maxLeaveFormBytes)

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// For non-admins, ignore the form-supplied member_id and
	// force it to the session's own member record. This closes the
	// privilege-escalation vector where a curious user POSTs
	// member_id=someone-elses-uuid to create a leave row on
	// someone else's behalf. Admins keep the form value as-is.
	//
	// In production, the safeRequireAuth middleware guarantees a
	// user is set; if it isn't, the route is being called from a
	// place that bypassed auth — refuse the request rather than
	// fall through to the unprotected form path.
	user, ok := auth.GetUserFromContext(ctx)
	if !ok || user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	memberID := r.FormValue("member_id")
	if !auth.IsAdminSession(user) {
		self := h.resolveMemberID(ctx, user.Email)
		if self == "" {
			http.Error(w, "no team member record for current user", http.StatusForbidden)
			return
		}
		memberID = self
	}

	startDate := r.PostForm.Get("start_date")
	endDate := r.PostForm.Get("end_date")

	// Validate dates before hitting the database.
	if err := validateLeaveDates(startDate, endDate); err != nil {
		h.renderLeaveReportForm(w, r, data, err.Error(), memberID, startDate, endDate)
		return
	}

	leaveID, err := h.db.CreateLeaveRecord(ctx, memberID, startDate, endDate)
	if err != nil {
		h.renderLeaveReportForm(w, r, data, err.Error(), memberID, startDate, endDate)
		return
	}

	if err := h.maintenance.HandleLeaveChange(ctx, leaveID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) renderLeaveReportForm(w http.ResponseWriter, r *http.Request, data map[string]any, errMsg, memberID, startDate, endDate string) {
	ctx := r.Context()

	members, err := h.db.GetActiveTeamMembers(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data["Members"] = members

	if errMsg != "" {
		data["Error"] = errMsg
		data["SelectedMemberID"] = memberID
		data["StartDate"] = startDate
		data["EndDate"] = endDate
	}

	if err := h.tmpl.ExecuteTemplate(w, "leave_report.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// validateLeaveDates validates leave date strings and their ordering.
func validateLeaveDates(startDate, endDate string) error {
	const dateLayout = "2006-01-02"

	startTime, err := time.Parse(dateLayout, startDate)
	if err != nil {
		return errors.New("invalid start_date format, expected YYYY-MM-DD")
	}

	endTime, err := time.Parse(dateLayout, endDate)
	if err != nil {
		return errors.New("invalid end_date format, expected YYYY-MM-DD")
	}

	if endTime.Before(startTime) {
		return errors.New("end_date must be on or after start_date")
	}

	return nil
}

func (h *Handler) handleLeaveManagement(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := map[string]any{
		"Template": "leave_management",
	}

	user, _ := auth.GetUserFromContext(ctx)
	isAdmin := auth.IsAdminSession(user)
	data["User"] = user
	data["IsAdmin"] = isAdmin

	// Non-admins see only their own leave; admins see everyone's.
	// The DB query is the same — we just narrow the result set in
	// the handler so the existing /leave/report flow and the past-
	// period purge still operate on the full table.
	scopeMemberID := ""
	if !isAdmin {
		scopeMemberID = h.resolveMemberID(ctx, user.Email)
		if scopeMemberID == "" {
			// Not a team member — the page is empty for them.
			data["Leaves"] = []enrichedLeave{}
			data["Members"] = []database.TeamMember{}
			data["SelfMemberID"] = ""
			if err := h.tmpl.ExecuteTemplate(w, "leave_management.html", data); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
		data["SelfMemberID"] = scopeMemberID
	}

	leaves, err := h.loadLeaveRowsForScope(ctx, scopeMemberID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Get all team members for the member-name lookup. The template
	// uses the list for the edit modal (admin only).
	members, _ := h.db.GetActiveTeamMembers(ctx)
	data["Members"] = members
	data["Leaves"] = h.enrichLeavesWithNames(ctx, leaves, members)

	if err := h.tmpl.ExecuteTemplate(w, "leave_management.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// loadLeaveRowsForScope returns the leave rows visible to the
// current user: all rows for admins, only the user's own for
// non-admins. Extracted from handleLeaveManagement so the cyclop
// budget on the page handler stays under control.
func (h *Handler) loadLeaveRowsForScope(ctx context.Context, scopeMemberID string) ([]database.LeaveRecord, error) {
	if scopeMemberID == "" {
		return h.db.GetLeaveRecords(ctx)
	}
	all, err := h.db.GetLeaveRecords(ctx)
	if err != nil {
		return nil, err
	}
	out := all[:0:0]
	for i := range all {
		if all[i].MemberID == scopeMemberID {
			out = append(out, all[i])
		}
	}
	return out, nil
}

// enrichLeavesWithNames joins the leave rows with their member
// records to populate the MemberName field the template displays.
// Extracted from handleLeaveManagement for the same reason as
// loadLeaveRowsForScope.
func (h *Handler) enrichLeavesWithNames(_ context.Context, leaves []database.LeaveRecord, members []database.TeamMember) []enrichedLeave {
	names := make(map[string]string, len(members))
	for _, m := range members {
		names[m.ID] = m.Name
	}
	out := make([]enrichedLeave, 0, len(leaves))
	for i := range leaves {
		name := "Unknown"
		if n, ok := names[leaves[i].MemberID]; ok {
			name = n
		}
		out = append(out, enrichedLeave{Leave: leaves[i], MemberName: name})
	}
	return out
}

func (h *Handler) handleLeaveEdit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	leaveID := chi.URLParam(r, "id")

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Authorize FIRST: only the leave's owner or an admin may edit.
	// Loading the existing row here means the ownership check
	// happens before any form parsing or DB write, so a non-admin
	// poking at the URL never touches a row they don't own.
	user, _ := auth.GetUserFromContext(ctx)
	isAdmin := auth.IsAdminSession(user)
	selfMemberID := h.resolveMemberID(ctx, user.Email)
	existing, err := h.db.GetLeaveByID(ctx, leaveID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !canMutateLeave(selfMemberID, existing.MemberID, isAdmin) {
		http.Error(w, "you can only edit your own leave records", http.StatusForbidden)
		return
	}

	// Parse and validate the form body.
	memberID, startDate, endDate, ok := parseLeaveEditForm(w, r, isAdmin, selfMemberID)
	if !ok {
		return
	}

	// existing (loaded during the auth check above) carries the row's
	// status, which is managed by the scheduling engine and must be
	// preserved across an edit.
	if err := h.db.UpdateLeaveRecord(ctx, leaveID, memberID, startDate, endDate, existing.Status); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Handle leave change using maintenance service.
	if err := h.maintenance.HandleLeaveChange(ctx, leaveID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/leave/manage", http.StatusSeeOther)
}

// parseLeaveEditForm parses the edit form body, validates the
// inputs, and returns the three values the handler needs. Extracted
// from handleLeaveEdit so the parent function stays under the
// cyclop 10 budget. On validation failure it writes the error to w
// and returns ok=false so the caller can just `return`.
func parseLeaveEditForm(w http.ResponseWriter, r *http.Request, isAdmin bool, selfMemberID string) (memberID, startDate, endDate string, ok bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxLeaveFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return "", "", "", false
	}

	// For non-admins, force the leave's member to themselves so a
	// tampered form value can't re-route the leave to someone else.
	memberID = strings.TrimSpace(r.PostForm.Get("member_id"))
	if !isAdmin {
		memberID = selfMemberID
	}
	startDate = strings.TrimSpace(r.PostForm.Get("start_date"))
	endDate = strings.TrimSpace(r.PostForm.Get("end_date"))

	if memberID == "" {
		http.Error(w, "member_id cannot be empty", http.StatusBadRequest)
		return "", "", "", false
	}
	if startDate == "" {
		http.Error(w, "start_date cannot be empty", http.StatusBadRequest)
		return "", "", "", false
	}
	if endDate == "" {
		http.Error(w, "end_date cannot be empty", http.StatusBadRequest)
		return "", "", "", false
	}

	if err := validateLeaveDates(startDate, endDate); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return "", "", "", false
	}

	return memberID, startDate, endDate, true
}

func (h *Handler) handleLeaveDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	leaveID := chi.URLParam(r, "id")

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Authorize FIRST: only the leave's owner or an admin may delete.
	user, _ := auth.GetUserFromContext(ctx)
	isAdmin := auth.IsAdminSession(user)
	selfMemberID := h.resolveMemberID(ctx, user.Email)
	existing, err := h.db.GetLeaveByID(ctx, leaveID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !canMutateLeave(selfMemberID, existing.MemberID, isAdmin) {
		http.Error(w, "you can only delete your own leave records", http.StatusForbidden)
		return
	}

	if err := h.maintenance.HandleLeaveDelete(ctx, leaveID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/leave/manage", http.StatusSeeOther)
}

// canMutateLeave reports whether the session member (sessionMemberID)
// is allowed to edit/delete the leave record owned by
// ownerMemberID. Admins can mutate any row; everyone else can only
// mutate their own.
func canMutateLeave(sessionMemberID string, ownerMemberID string, isAdmin bool) bool {
	if isAdmin {
		return true
	}
	return sessionMemberID != "" && sessionMemberID == ownerMemberID
}
