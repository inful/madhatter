-- Notification logs

-- name: CreateNotificationLog :execrows
INSERT OR IGNORE INTO notification_logs (
  id,
  kind,
  date,
  member_id,
  assignment_id,
  message
) VALUES (?, ?, ?, ?, ?, ?);

-- name: DeleteNotificationLog :exec
DELETE FROM notification_logs WHERE id = ?;
