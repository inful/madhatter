package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/inful/madhatter/internal/auth"
)

const (
	maxRestoreUploadBytes   = 50 << 20
	maxMultipartMemoryLimit = 32 << 10
	pendingRestoreTokenTTL  = 30 * time.Minute
)

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
		h.cleanupExpiredPendingRestores(time.Now())
		h.renderDatabaseRestore(w, r, "", false, "")
	case http.MethodPost:
		h.handleDatabaseRestorePost(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleDatabaseRestorePost(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRestoreUploadBytes)
	if err := r.ParseMultipartForm(maxMultipartMemoryLimit); err != nil { //nolint:gosec // bounded by MaxBytesReader above
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	mode := r.FormValue("mode")
	validatedToken := strings.TrimSpace(r.FormValue("validated_token"))

	if mode == "apply" && validatedToken != "" {
		h.handleDatabaseRestoreApplyWithToken(w, r, validatedToken)
		return
	}

	content, err := h.readRestoreUpload(w, r)
	if err != nil {
		return
	}

	if mode == "apply" {
		h.handleDatabaseRestoreApply(w, r, content, "")
		return
	}

	validateErr := h.db.ValidateRestoreCandidate(r.Context(), content)
	if validateErr != nil {
		h.renderDatabaseRestore(w, r, "Validation failed: "+validateErr.Error(), false, "")
		return
	}

	validatedToken, err = h.storePendingRestore(content)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.renderDatabaseRestore(w, r, "Validation succeeded. You can apply now without re-uploading.", true, validatedToken)
}

func (h *Handler) handleDatabaseRestoreApplyWithToken(w http.ResponseWriter, r *http.Request, validatedToken string) {
	if r.FormValue("confirm_overwrite") != "on" {
		h.renderDatabaseRestore(w, r, "Restore blocked: you must confirm overwrite before applying.", false, validatedToken)
		return
	}

	backupBytes, err := h.consumePendingRestore(validatedToken, time.Now())
	if err != nil {
		h.renderDatabaseRestore(w, r, "Validated file expired or unavailable. Please validate again.", false, "")
		return
	}

	h.handleDatabaseRestoreApply(w, r, backupBytes, "")
}

func (h *Handler) readRestoreUpload(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	file, _, err := r.FormFile("backup_file")
	if err != nil {
		http.Error(w, "backup_file is required", http.StatusBadRequest)
		return nil, err
	}
	defer func() {
		_ = file.Close()
	}()

	content, err := io.ReadAll(io.LimitReader(file, maxRestoreUploadBytes+1))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return nil, err
	}

	if int64(len(content)) > maxRestoreUploadBytes {
		http.Error(w, "uploaded file exceeds maximum allowed size", http.StatusRequestEntityTooLarge)
		return nil, errors.New("uploaded file exceeds maximum allowed size")
	}

	return content, nil
}

func (h *Handler) handleDatabaseRestoreApply(w http.ResponseWriter, r *http.Request, backupBytes []byte, validatedToken string) {
	if r.FormValue("confirm_overwrite") != "on" {
		h.renderDatabaseRestore(w, r, "Restore blocked: you must confirm overwrite before applying.", false, validatedToken)
		return
	}

	h.restoreMu.Lock()
	defer h.restoreMu.Unlock()

	if h.restoreBusy.Load() {
		http.Error(w, "A restore is already in progress", http.StatusConflict)
		return
	}

	h.restoreBusy.Store(true)
	defer h.restoreBusy.Store(false)

	if err := h.db.ApplyRestoreCandidate(r.Context(), backupBytes); err != nil {
		h.renderDatabaseRestore(w, r, "Restore failed: "+err.Error(), false, validatedToken)
		return
	}

	if validatedToken != "" {
		h.deletePendingRestore(validatedToken)
	}

	h.renderDatabaseRestore(w, r, "Restore applied successfully.", true, "")
}

func (h *Handler) renderDatabaseRestore(w http.ResponseWriter, r *http.Request, message string, ok bool, validatedToken string) {
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

	if validatedToken != "" {
		data["ValidatedRestoreToken"] = validatedToken
	}

	if err := h.tmpl.ExecuteTemplate(w, "database_restore.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) storePendingRestore(content []byte) (string, error) {
	tmpFile, err := os.CreateTemp("", "madhatter-validated-restore-*.db")
	if err != nil {
		return "", err
	}
	defer func() {
		_ = tmpFile.Close()
	}()

	if _, err := tmpFile.Write(content); err != nil {
		return "", err
	}

	if err := tmpFile.Sync(); err != nil {
		return "", err
	}

	token := uuid.NewString()
	h.pendingMu.Lock()
	h.cleanupExpiredPendingRestoresLocked(time.Now())
	h.pendingRestore[token] = pendingRestoreItem{
		Path:      tmpFile.Name(),
		CreatedAt: time.Now(),
	}
	h.pendingMu.Unlock()

	return token, nil
}

func (h *Handler) consumePendingRestore(token string, now time.Time) ([]byte, error) {
	h.pendingMu.Lock()
	h.cleanupExpiredPendingRestoresLocked(now)
	item, ok := h.pendingRestore[token]
	if ok {
		delete(h.pendingRestore, token)
	}
	h.pendingMu.Unlock()
	if !ok {
		return nil, os.ErrNotExist
	}

	defer func() {
		_ = os.Remove(item.Path)
	}()

	return os.ReadFile(item.Path)
}

func (h *Handler) deletePendingRestore(token string) {
	h.pendingMu.Lock()
	item, ok := h.pendingRestore[token]
	if ok {
		delete(h.pendingRestore, token)
	}
	h.pendingMu.Unlock()

	if ok {
		_ = os.Remove(item.Path)
	}
}

func (h *Handler) cleanupExpiredPendingRestores(now time.Time) {
	h.pendingMu.Lock()
	h.cleanupExpiredPendingRestoresLocked(now)
	h.pendingMu.Unlock()
}

func (h *Handler) cleanupExpiredPendingRestoresLocked(now time.Time) {
	for token, item := range h.pendingRestore {
		if now.Sub(item.CreatedAt) <= pendingRestoreTokenTTL {
			continue
		}

		delete(h.pendingRestore, token)
		_ = os.Remove(item.Path)
	}
}
