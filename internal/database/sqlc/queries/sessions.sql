-- name: CreateSession :one
INSERT INTO sessions (
    id, user_id, token, expires_at
) VALUES (
    ?, ?, ?, ?
)
RETURNING *;

-- name: GetSessionByToken :one
SELECT s.*, u.email, u.name, u.is_admin 
FROM sessions s
JOIN users u ON s.user_id = u.id
WHERE s.token = ? AND s.expires_at > CURRENT_TIMESTAMP;

-- name: DeleteSession :exec
DELETE FROM sessions 
WHERE token = ?;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions 
WHERE expires_at <= CURRENT_TIMESTAMP;

-- name: DeleteUserSessions :exec
DELETE FROM sessions 
WHERE user_id = ?;