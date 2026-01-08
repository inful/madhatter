-- name: GetUserByID :one
SELECT * FROM users 
WHERE id = ? 
LIMIT 1;

-- name: GetUserByEmail :one
SELECT * FROM users 
WHERE email = ? 
LIMIT 1;

-- name: GetUserByProvider :one
SELECT * FROM users 
WHERE provider = ? AND provider_id = ? 
LIMIT 1;

-- name: CreateUser :one
INSERT INTO users (
    id, email, name, provider, provider_id, is_admin, is_active
) VALUES (
    ?, ?, ?, ?, ?, ?, ?
)
RETURNING *;

-- name: UpdateUser :exec
UPDATE users 
SET 
    name = ?,
    is_admin = ?,
    is_active = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: UpdateUserProvider :exec
UPDATE users 
SET 
    name = ?,
    provider = ?,
    provider_id = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: ListActiveUsers :many
SELECT * FROM users 
WHERE is_active = 1 
ORDER BY name;

-- name: ListAdminUsers :many
SELECT * FROM users 
WHERE is_admin = 1 AND is_active = 1 
ORDER BY name;

-- name: CountAdmins :one
SELECT COUNT(*) FROM users 
WHERE is_admin = 1 AND is_active = 1;