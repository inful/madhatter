-- name: GetOAuthToken :one
SELECT * FROM oauth_tokens 
WHERE user_id = ? AND provider = ? 
LIMIT 1;

-- name: CreateOAuthToken :one
INSERT INTO oauth_tokens (
    id, user_id, provider, access_token, refresh_token, token_type, expires_at
) VALUES (
    ?, ?, ?, ?, ?, ?, ?
)
RETURNING *;

-- name: UpdateOAuthToken :exec
UPDATE oauth_tokens 
SET 
    access_token = ?,
    refresh_token = ?,
    token_type = ?,
    expires_at = ?,
    updated_at = CURRENT_TIMESTAMP
WHERE user_id = ? AND provider = ?;

-- name: DeleteOAuthToken :exec
DELETE FROM oauth_tokens
WHERE user_id = ? AND provider = ?;

-- name: DeleteUserOAuthTokens :exec
DELETE FROM oauth_tokens
WHERE user_id = ?;