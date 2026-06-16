-- name: CreateSession :one
INSERT INTO sessions (
    id, user_id, token, expires_at
) VALUES (
    ?, ?, ?, ?
)
RETURNING *;

-- name: GetSessionByToken :one
SELECT s.*, u.email, u.name, u.is_admin, u.is_active
FROM sessions s
JOIN users u ON s.user_id = u.id
WHERE s.token = ? AND datetime(s.expires_at) > datetime('now');

-- name: DeleteSession :exec
DELETE FROM sessions 
WHERE token = ?;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions 
WHERE datetime(expires_at) <= datetime('now');

-- name: DeleteUserSessions :exec
DELETE FROM sessions 
WHERE user_id = ?;