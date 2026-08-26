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

	h.renderLeaveReportForm(w, r, data, "", "", "", "", "")
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

	// Accept the typed value as-is; CreateLeaveRecord defaults bad
	// input to plain leave so a malformed form value can't fail the
	// write with an opaque DB error.
	leaveType := r.FormValue("leave_type")

	// Validate dates before hitting the database.
	if err := validateLeaveDates(startDate, endDate); err != nil {
		h.renderLeaveReportForm(w, r, data, err.Error(), memberID, startDate, endDate, leaveType)
		return
	}

	leaveID, err := h.db.CreateLeaveRecord(ctx, memberID, startDate, endDate, leaveType)
	if err != nil {
		h.renderLeaveReportForm(w, r, data, err.Error(), memberID, startDate, endDate, leaveType)
		return
	}

	if err := h.maintenance.HandleLeaveChange(ctx, leaveID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) renderLeaveReportForm(w http.ResponseWriter, r *http.Request, data map[string]any, errMsg, memberID, startDate, endDate, leaveType string) {
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
		// Default to plain leave so the dropdown renders cleanly when
		// the form is re-rendered without a prior user choice.
		if leaveType == "" {
			leaveType = database.LeaveTypeLeave
		}
		data["SelectedLeaveType"] = leaveType
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
	memberID, startDate, endDate, leaveType, ok := parseLeaveEditForm(w, r, isAdmin, selfMemberID, existing.LeaveType)
	if !ok {
		return
	}

	// existing (loaded during the auth check above) carries the row's
	// status, which is managed by the scheduling engine and must be
	// preserved across an edit.
	if err := h.db.UpdateLeaveRecord(ctx, leaveID, memberID, startDate, endDate, existing.Status, leaveType); err != nil {
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
// inputs, and returns the values the handler needs. Extracted from
// handleLeaveEdit so the parent function stays under the cyclop 10
// budget. On validation failure it writes the error to w and returns
// ok=false so the caller can just `return`.
//
// existingLeaveType is the leave_type currently stored on the row. If
// the form doesn't send a leave_type value (older clients, hand-rolled
// curl), fall back to that so an edit doesn't silently reset the type
// to plain leave.
func parseLeaveEditForm(w http.ResponseWriter, r *http.Request, isAdmin bool, selfMemberID, existingLeaveType string) (memberID, startDate, endDate, leaveType string, ok bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxLeaveFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return "", "", "", "", false
	}

	// For non-admins, force the leave's member to themselves so a
	// tampered form value can't re-route the leave to someone else.
	memberID = strings.TrimSpace(r.PostForm.Get("member_id"))
	if !isAdmin {
		memberID = selfMemberID
	}
	startDate = strings.TrimSpace(r.PostForm.Get("start_date"))
	endDate = strings.TrimSpace(r.PostForm.Get("end_date"))
	leaveType = strings.TrimSpace(r.PostForm.Get("leave_type"))
	if leaveType == "" {
		leaveType = existingLeaveType
	}

	if memberID == "" {
		http.Error(w, "member_id cannot be empty", http.StatusBadRequest)
		return "", "", "", "", false
	}
	if startDate == "" {
		http.Error(w, "start_date cannot be empty", http.StatusBadRequest)
		return "", "", "", "", false
	}
	if endDate == "" {
		http.Error(w, "end_date cannot be empty", http.StatusBadRequest)
		return "", "", "", "", false
	}

	if err := validateLeaveDates(startDate, endDate); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return "", "", "", "", false
	}

	return memberID, startDate, endDate, leaveType, true
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

// handleLeaveReportSick serves the same-day "someone called in sick"
// form. Visible to every authenticated user: a non-admin user can
// register a leave row for a *different* team member pinned to
// today's date, while admins continue to use the unrestricted
// /leave/report flow for arbitrary dates. The date lock is what
// scopes the relaxation — start_date == end_date == today is
// enforced on POST regardless of the form's hidden inputs, so a
// tampered request cannot file a leave for a future or past day.
//
// This is a *create* path that intentionally widens who can act on
// whose behalf relative to /leave/report, so it carries the same
// per-user-data mutation safeguards as the rest of the leave code:
// 401 on a missing session, ownership-checked re-rendering on bad
// input, and a duplicate guard so a non-admin doesn't accidentally
// stack rows.
func (h *Handler) handleLeaveReportSick(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := map[string]any{
		"Template": "leave_report_sick",
	}

	if user, ok := auth.GetUserFromContext(ctx); ok {
		data["User"] = user
		data["IsAdmin"] = auth.IsAdminSession(user)
	}

	today := time.Now().Format("2006-01-02")

	if r.Method == http.MethodPost {
		h.handleLeaveReportSickPost(w, r, data, today)
		return
	}

	h.renderLeaveReportSickForm(w, r, data, sickLeaveFormValues{Today: today})
}

// handleLeaveReportSickPost writes the sick-leave-for-someone-else
// row. Extracted from handleLeaveReportSick so the page handler
// stays under the cyclop 10 budget. On success it runs the same
// maintenance path as /leave/report so cover assignment runs.
//
// The leave_type is hardcoded to LeaveTypeLeave — the same-day sick
// path doesn't take a type selector, and any leave_type POSTed by a
// tamper-prone client is silently overridden. The /leave/report flow
// keeps the type selector; the relax-only-for-sick use case doesn't
// need it.
func (h *Handler) handleLeaveReportSickPost(w http.ResponseWriter, r *http.Request, data map[string]any, today string) {
	ctx := r.Context()

	// Per AGENTS.md, a missing session is a hard 401 — the safeRequireAuth
	// middleware guarantees a user in production; this is defense in depth
	// against middleware bypass or future refactors.
	user, ok := auth.GetUserFromContext(ctx)
	if !ok || user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	memberID, ok := parseSickLeaveForm(w, r)
	if !ok {
		return
	}

	if memberID == "" {
		h.renderLeaveReportSickForm(w, r, data, sickLeaveFormValues{
			ErrMsg:   "team member is required",
			MemberID: memberID,
			Today:    today,
		})
		return
	}

	if !h.ensureSickLeaveWritable(w, r, data, memberID, today) {
		return
	}

	leaveID, err := h.db.CreateLeaveRecord(ctx, memberID, today, today, database.LeaveTypeLeave)
	if err != nil {
		h.renderLeaveReportSickForm(w, r, data, sickLeaveFormValues{
			ErrMsg:   err.Error(),
			MemberID: memberID,
			Today:    today,
		})
		return
	}

	if err := h.maintenance.HandleLeaveChange(ctx, leaveID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// ensureSickLeaveWritable validates a sick-leave POST body end-to-end
// (date lock → member lookup → duplicate guard) and renders the form
// with the relevant error message on any failure. Returns true when
// the request is safe to write.
//
// Extracted from handleLeaveReportSickPost so the parent stays
// under cyclop.
func (h *Handler) ensureSickLeaveWritable(w http.ResponseWriter, r *http.Request, data map[string]any, memberID, today string) bool {
	ctx := r.Context()

	// Date lock: the form's hidden inputs may be tampered with, so we
	// re-validate against the server-computed today before touching the DB.
	if err := validateSickLeaveDates(r.PostForm.Get("start_date"), r.PostForm.Get("end_date"), today); err != nil {
		h.renderLeaveReportSickForm(w, r, data, sickLeaveFormValues{
			ErrMsg: err.Error(), MemberID: memberID, Today: today,
		})
		return false
	}

	member, err := h.db.GetMemberByID(ctx, memberID)
	if err != nil || member == nil {
		h.renderLeaveReportSickForm(w, r, data, sickLeaveFormValues{
			ErrMsg: "selected team member was not found", MemberID: memberID, Today: today,
		})
		return false
	}

	// Duplicate guard: refuse to stack a second leave row for the
	// same (member, today). Without this, hitting submit twice would
	// create two parallel rows — confusing for the cover-assignment
	// engine and unhelpful for the user who isn't going to manage the
	// page to clean them up.
	dup, err := h.hasSickLeaveDuplicate(ctx, memberID, today)
	if err != nil {
		h.renderLeaveReportSickForm(w, r, data, sickLeaveFormValues{
			ErrMsg: err.Error(), MemberID: memberID, Today: today,
		})
		return false
	}
	if dup {
		h.renderLeaveReportSickForm(w, r, data, sickLeaveFormValues{
			ErrMsg: member.Name + " already has a leave record for today", MemberID: memberID, Today: today,
		})
		return false
	}

	return true
}

// sickLeaveFormValues is the bundle renderLeaveReportSickForm needs
// to repopulate the form on a validation failure. Pulled out as a
// struct so the call sites don't drown in positional arguments and
// so adding a new field is a single-site change.
type sickLeaveFormValues struct {
	ErrMsg   string
	MemberID string
	Today    string
}

// parseSickLeaveForm parses the sick-leave form body and captures
// the member_id value; returns ok=false on a form parse error
// (after writing a 400). Validation of member_id happens in the
// caller so the form can be re-rendered with the user's selection
// preserved.
//
// Extracted from handleLeaveReportSickPost so the parent stays
// under cyclop.
func parseSickLeaveForm(w http.ResponseWriter, r *http.Request) (memberID string, ok bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxLeaveFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return "", false
	}
	return strings.TrimSpace(r.PostForm.Get("member_id")), true
}

// validateSickLeaveDates accepts only start_date == end_date == today.
// The route's whole point is the same-day lock — any other shape is
// a bug or an attack.
func validateSickLeaveDates(startDate, endDate, today string) error {
	const dateLayout = "2006-01-02"

	if _, err := time.Parse(dateLayout, startDate); err != nil {
		return errors.New("start_date must be today")
	}
	if _, err := time.Parse(dateLayout, endDate); err != nil {
		return errors.New("end_date must be today")
	}
	if startDate != today {
		return errors.New("start_date must be today")
	}
	if endDate != today {
		return errors.New("end_date must be today")
	}
	return nil
}

// hasSickLeaveDuplicate reports whether a leave row already exists
// for (memberID, today). Used to refuse a second submission; the
// error from the DB lookup is propagated so genuine DB failures
// surface to the user rather than being silently treated as
// "no duplicate".
func (h *Handler) hasSickLeaveDuplicate(ctx context.Context, memberID, today string) (bool, error) {
	rows, err := h.db.GetLeaveByDate(ctx, today)
	if err != nil {
		return false, err
	}
	for i := range rows {
		if rows[i].MemberID == memberID {
			return true, nil
		}
	}
	return false, nil
}

// renderLeaveReportSickForm renders the form, optionally with an
// error message and the user's prior member selection preserved.
// The picker shows the member's display name only — emails are not
// surfaced on this page.
func (h *Handler) renderLeaveReportSickForm(w http.ResponseWriter, r *http.Request, data map[string]any, vals sickLeaveFormValues) {
	ctx := r.Context()

	members, err := h.db.GetActiveTeamMembers(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data["Members"] = members

	if vals.ErrMsg != "" {
		data["Error"] = vals.ErrMsg
		data["SelectedMemberID"] = vals.MemberID
	}
	data["Today"] = vals.Today
	data["StartDate"] = vals.Today
	data["EndDate"] = vals.Today

	if err := h.tmpl.ExecuteTemplate(w, "leave_report_sick.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
