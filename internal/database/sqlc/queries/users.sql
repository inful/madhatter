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
-- Inserts a user as pending (is_active = 0). The first-ever
-- user is bootstrapped via CreateUserAsFirstAdmin instead, which
-- sets is_active = 1 atomically.
INSERT INTO users (
    id, email, name, provider, provider_id, is_admin, is_active
) VALUES (
    ?, ?, ?, ?, ?, ?, 0
)
RETURNING *;

-- name: CreateActiveUser :one
-- Inserts an already-active user. Used by test fixtures and the
-- dev-mode seeder; the production OAuth flow goes through
-- CreateUser (pending) and is approved by an admin.
INSERT INTO users (
    id, email, name, provider, provider_id, is_admin, is_active
) VALUES (
    ?, ?, ?, ?, ?, ?, 1
)
RETURNING *;

-- name: CreateUserAsFirstAdmin :one
-- Atomically creates a user and makes them admin only if no admins
-- exist. The first-ever user is bootstrapped active (is_active = 1)
-- so the operator can log in; every subsequent user is pending
-- (is_active = 0) and requires admin approval.
INSERT INTO users (
    id, email, name, provider, provider_id, is_admin, is_active
)
SELECT ?, ?, ?, ?, ?,
    CASE WHEN (SELECT COUNT(*) FROM users WHERE is_admin = 1 AND is_active = 1) = 0 THEN 1 ELSE 0 END,
    CASE WHEN (SELECT COUNT(*) FROM users WHERE is_admin = 1 AND is_active = 1) = 0 THEN 1 ELSE 0 END
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

-- name: ListPendingUsers :many
-- Users awaiting admin approval. Excludes admin-deactivated
-- users (those have deactivated_at set).
SELECT * FROM users
WHERE is_active = 0 AND deactivated_at IS NULL
ORDER BY created_at;

-- name: ListDeactivatedUsers :many
SELECT * FROM users
WHERE is_active = 0 AND deactivated_at IS NOT NULL
ORDER BY deactivated_at DESC;

-- name: ListAdminUsers :many
SELECT * FROM users
WHERE is_admin = 1 AND is_active = 1
ORDER BY name;

-- name: CountAdmins :one
SELECT COUNT(*) FROM users
WHERE is_admin = 1 AND is_active = 1;

-- name: CountPendingUsers :one
SELECT COUNT(*) FROM users
WHERE is_active = 0 AND deactivated_at IS NULL;

-- name: ApproveUser :one
-- Activates a pending user. The deactivated_at column stays NULL
-- (it was NULL while pending and is cleared on approve).
UPDATE users
SET is_active = 1, deactivated_at = NULL, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING *;

-- name: DeactivateUser :one
-- Admin-initiated deactivation of an active user. Sets
-- deactivated_at; the team page can offer reactivate from this
-- state.
UPDATE users
SET is_active = 0, deactivated_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING *;

-- name: ReactivateUser :one
UPDATE users
SET is_active = 1, deactivated_at = NULL, updated_at = CURRENT_TIMESTAMP
WHERE id = ?
RETURNING *;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = ?;