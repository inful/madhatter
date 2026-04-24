package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/inful/madhatter/internal/auth"
	"github.com/inful/madhatter/internal/database"
)

const maxLeaveFormBytes = 1 << 20

func (h *Handler) handleLeaveReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := map[string]any{
		"Template": "leave_report",
	}

	// Add user info to data.
	if user, ok := auth.GetUserFromContext(ctx); ok {
		data["User"] = user
		data["IsAdmin"] = user.IsAdmin.Valid && user.IsAdmin.Int64 == 1
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

	memberID := r.FormValue("member_id")
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

	// Add user info to data.
	if user, ok := auth.GetUserFromContext(ctx); ok {
		data["User"] = user
		data["IsAdmin"] = user.IsAdmin.Valid && user.IsAdmin.Int64 == 1
	}

	// Get all leave records.
	leaves, err := h.db.GetLeaveRecords(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Get all team members to enrich leave data.
	members, err := h.db.GetActiveTeamMembers(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Create member map for quick lookup.
	memberMap := make(map[string]database.TeamMember)
	for _, m := range members {
		memberMap[m.ID] = m
	}

	// Enrich leaves with member details.
	type enrichedLeave struct {
		Leave      database.LeaveRecord
		MemberName string
	}

	enrichedLeaves := make([]enrichedLeave, 0, len(leaves))
	for i := range leaves {
		el := enrichedLeave{
			Leave:      leaves[i],
			MemberName: "Unknown",
		}
		if member, ok := memberMap[leaves[i].MemberID]; ok {
			el.MemberName = member.Name
		}
		enrichedLeaves = append(enrichedLeaves, el)
	}

	data["Leaves"] = enrichedLeaves
	data["Members"] = members

	if err := h.tmpl.ExecuteTemplate(w, "leave_management.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) handleLeaveEdit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	leaveID := chi.URLParam(r, "id")

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxLeaveFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	memberID := strings.TrimSpace(r.PostForm.Get("member_id"))
	startDate := strings.TrimSpace(r.PostForm.Get("start_date"))
	endDate := strings.TrimSpace(r.PostForm.Get("end_date"))

	// Validate input at handler level.
	if memberID == "" {
		http.Error(w, "member_id cannot be empty", http.StatusBadRequest)
		return
	}
	if startDate == "" {
		http.Error(w, "start_date cannot be empty", http.StatusBadRequest)
		return
	}
	if endDate == "" {
		http.Error(w, "end_date cannot be empty", http.StatusBadRequest)
		return
	}

	// Validate date format and ordering.
	if err := validateLeaveDates(startDate, endDate); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Preserve the existing status — it is managed by the scheduling engine.
	existing, err := h.db.GetLeaveByID(ctx, leaveID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

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

func (h *Handler) handleLeaveDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	leaveID := chi.URLParam(r, "id")

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := h.db.DeleteLeaveRecord(ctx, leaveID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Reconcile covers - will remove any stale covers automatically.
	if err := h.maintenance.HandleTeamChange(ctx); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/leave/manage", http.StatusSeeOther)
}
