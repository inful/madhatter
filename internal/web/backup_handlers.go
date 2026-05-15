package web

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/inful/madhatter/internal/auth"
)

const maxRestoreUploadBytes = 50 << 20

func (h *Handler) handleDatabaseBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	user := mustGetUser(r.Context())
	if !auth.IsAdminSession(user) {
		http.Error(w, "Admin access required", http.StatusForbidden)
		return
	}

	backupBytes, err := h.db.CreateBackup(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	fileName := fmt.Sprintf("madhatter-backup-%s.db", time.Now().UTC().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fileName))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(backupBytes)
}

func (h *Handler) handleDatabaseRestore(w http.ResponseWriter, r *http.Request) {
	user := mustGetUser(r.Context())
	if !auth.IsAdminSession(user) {
		http.Error(w, "Admin access required", http.StatusForbidden)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.renderDatabaseRestore(w, r, "", false)
	case http.MethodPost:
		h.handleDatabaseRestoreValidation(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleDatabaseRestoreValidation(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRestoreUploadBytes)
	if err := r.ParseMultipartForm(maxRestoreUploadBytes); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	file, _, err := r.FormFile("backup_file")
	if err != nil {
		http.Error(w, "backup_file is required", http.StatusBadRequest)
		return
	}
	defer func() {
		_ = file.Close()
	}()

	content, err := io.ReadAll(io.LimitReader(file, maxRestoreUploadBytes+1))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if int64(len(content)) > maxRestoreUploadBytes {
		http.Error(w, "uploaded file exceeds maximum allowed size", http.StatusRequestEntityTooLarge)
		return
	}

	if err := h.db.ValidateRestoreCandidate(r.Context(), content); err != nil {
		h.renderDatabaseRestore(w, r, "Validation failed: "+err.Error(), false)
		return
	}

	h.renderDatabaseRestore(w, r, "Validation succeeded. This file is compatible for restore.", true)
}

func (h *Handler) renderDatabaseRestore(w http.ResponseWriter, r *http.Request, message string, ok bool) {
	ctx := r.Context()
	data := map[string]any{
		"Template": "database_restore",
	}

	if user, hasUser := auth.GetUserFromContext(ctx); hasUser {
		data["User"] = user
		data["IsAdmin"] = auth.IsAdminSession(user)
	}

	if message != "" {
		data["ValidationMessage"] = message
		data["ValidationOK"] = ok
	}

	if err := h.tmpl.ExecuteTemplate(w, "database_restore.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
