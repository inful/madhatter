-- name: GetNotificationPreference :one
-- Returns the row for a member, or sql.ErrNoRows if no preference
-- has been set. The application treats the absence of a row as
-- "default" (email_enabled = 1).
SELECT member_id, email_enabled, disabled_at, updated_at
FROM notification_preferences
WHERE member_id = ?;

-- name: SetNotificationEmailEnabled :exec
-- Upserts the email-enabled flag for a member. Pass 1 to enable,
-- 0 to disable. disabled_at is set/cleared by the application
-- before calling this query.
INSERT INTO notification_preferences (member_id, email_enabled, disabled_at, updated_at)
VALUES (?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT (member_id) DO UPDATE SET
    email_enabled = excluded.email_enabled,
    disabled_at   = excluded.disabled_at,
    updated_at    = CURRENT_TIMESTAMP;

-- name: IsNotificationEmailEnabled :one
-- Returns 1 when the member has not disabled email, 0 when they
-- have. Returns 1 by default (no row) so callers don't have to
-- special-case "no preference set yet". The query is a single
-- parameter and a single scan, so the no-row case still produces
-- one row from the COALESCE branch.
SELECT COALESCE(
    (SELECT CASE WHEN email_enabled = 0 THEN 0 ELSE 1 END
     FROM notification_preferences
     WHERE member_id = ?),
    1
) AS is_enabled;
