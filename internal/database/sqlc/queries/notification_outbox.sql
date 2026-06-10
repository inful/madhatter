-- name: CreateOutboxEntry :execresult
INSERT INTO notification_outbox (
    id, event_kind, channel, recipient, recipient_name, subject, body
) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ClaimDueOutboxEntries :many
-- Atomically claim a batch of due rows by bumping next_attempt_at far into
-- the future, so concurrent workers don't pick the same row.
--
-- The datetime() wrapper normalises the stored ISO-8601 value (which the
-- ncruces/go-sqlite3 driver writes for time.Time arguments) into the
-- same YYYY-MM-DD HH:MM:SS form that datetime('now') returns, so the
-- comparison is meaningful. Without this normalisation SQLite compares
-- strings and 'T' > ' ' lexically, so no row would ever be due.
SELECT id, event_kind, channel, recipient, recipient_name, subject, body,
       attempts, last_error, next_attempt_at, status, created_at, sent_at
FROM notification_outbox
WHERE status = 'pending'
  AND datetime(next_attempt_at) <= datetime('now')
ORDER BY next_attempt_at
LIMIT ?;

-- name: MarkOutboxSent :execresult
UPDATE notification_outbox
SET status = 'sent', sent_at = CURRENT_TIMESTAMP, last_error = NULL
WHERE id = ?;

-- name: MarkOutboxFailed :execresult
UPDATE notification_outbox
SET attempts = attempts + 1,
    last_error = ?,
    next_attempt_at = ?
WHERE id = ?;

-- name: MarkOutboxDead :execresult
UPDATE notification_outbox
SET status = 'dead', attempts = attempts + 1,
    last_error = ?, next_attempt_at = ?
WHERE id = ?;

-- name: GetOutboxEntry :one
SELECT id, event_kind, channel, recipient, recipient_name, subject, body,
       attempts, last_error, next_attempt_at, status, created_at, sent_at
FROM notification_outbox
WHERE id = ?;
