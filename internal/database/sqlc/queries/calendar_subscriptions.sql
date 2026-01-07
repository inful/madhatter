-- name: CreateCalendarSubscription :execresult
INSERT INTO calendar_subscriptions (id, member_id, token)
VALUES (?, ?, ?);

-- name: GetSubscriptionByToken :one
SELECT id, member_id, token, created_at
FROM calendar_subscriptions
WHERE token = ?;

-- name: GetSubscriptionsByMemberID :many
SELECT id, member_id, token, created_at
FROM calendar_subscriptions
WHERE member_id = ?;

-- name: DeleteCalendarSubscription :exec
DELETE FROM calendar_subscriptions
WHERE token = ?;

-- name: DeleteMemberSubscriptions :exec
DELETE FROM calendar_subscriptions
WHERE member_id = ?;