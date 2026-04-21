-- name: CreateCalendarSubscription :execresult
INSERT INTO calendar_subscriptions (id, member_id, token)
VALUES (?, ?, ?);

-- name: GetSubscriptionByToken :one
SELECT id, member_id, token, created_at, last_used_at, last_used_rota_at, last_used_meetings_at
FROM calendar_subscriptions
WHERE token = ?;

-- name: GetSubscriptionsByMemberID :many
SELECT id, member_id, token, created_at, last_used_at, last_used_rota_at, last_used_meetings_at
FROM calendar_subscriptions
WHERE member_id = ?;

-- name: DeleteCalendarSubscription :exec
DELETE FROM calendar_subscriptions
WHERE token = ?;

-- name: DeleteMemberSubscriptions :exec
DELETE FROM calendar_subscriptions
WHERE member_id = ?;

-- name: TouchRotaSubscription :exec
UPDATE calendar_subscriptions
SET last_used_at = CURRENT_TIMESTAMP,
    last_used_rota_at = CURRENT_TIMESTAMP
WHERE token = ?;

-- name: TouchMeetingsSubscription :exec
UPDATE calendar_subscriptions
SET last_used_at = CURRENT_TIMESTAMP,
    last_used_meetings_at = CURRENT_TIMESTAMP
WHERE token = ?;

-- name: DeleteStaleSubscriptions :execresult
DELETE FROM calendar_subscriptions
WHERE last_used_at < ?
   OR (last_used_at IS NULL AND created_at < ?);

-- name: GetAllSubscriptions :many
SELECT id, member_id, token, created_at, last_used_at, last_used_rota_at, last_used_meetings_at
FROM calendar_subscriptions
ORDER BY last_used_at ASC NULLS FIRST;
