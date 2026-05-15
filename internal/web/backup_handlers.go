package web

import (
	"fmt"
	"net/http"
	"time"

	"github.com/inful/madhatter/internal/auth"
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
