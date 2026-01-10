-- name: CreateAPIToken :execresult
INSERT INTO api_tokens (
    id, user_id, name, token_hash, is_active, expires_at
) VALUES (
    ?, ?, ?, ?, ?, ?
);

-- name: GetAPITokenByHash :one
SELECT 
    id, user_id, name, token_hash, is_active, created_at, expires_at, last_used_at
FROM api_tokens
WHERE token_hash = ? AND is_active = 1
    AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP);

-- name: GetAPITokensByUser :many
SELECT 
    id, user_id, name, token_hash, is_active, created_at, expires_at, last_used_at
FROM api_tokens
WHERE user_id = ?
ORDER BY created_at DESC;

-- name: GetAPITokenByID :one
SELECT 
    id, user_id, name, token_hash, is_active, created_at, expires_at, last_used_at
FROM api_tokens
WHERE id = ?;

-- name: UpdateAPITokenLastUsed :execresult
UPDATE api_tokens
SET last_used_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: DeactivateAPIToken :execresult
UPDATE api_tokens
SET is_active = 0
WHERE id = ?;

-- name: DeleteAPIToken :execresult
DELETE FROM api_tokens
WHERE id = ?;

-- name: CleanupExpiredTokens :execresult
DELETE FROM api_tokens
WHERE expires_at < CURRENT_TIMESTAMP AND expires_at IS NOT NULL;