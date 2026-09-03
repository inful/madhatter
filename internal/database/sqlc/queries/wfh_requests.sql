-- name: DenyWFHRequest :execresult
UPDATE wfh_requests SET status = 'denied', settled_at = ?, denial_reason = ? WHERE id = ?;
-- Mark a request as denied and record the human-readable reason in
-- wfh_requests.denial_reason. The reason rides the same row to the
-- dashboard, the WFH list page, the admin manage page, and the
-- email notification so the user is never left guessing why their
-- request was rejected.
-- name: CreateWFHRequest :execresult
INSERT INTO wfh_requests (id, member_id, date, status)
VALUES (?, ?, ?, 'pending');

-- name: MarkAdminWFH :execresult
-- Insert an admin-marked WFH row in one shot. is_admin_marked=1 and
-- status='approved' so the row participates in every existing
-- query (quota, floor, ICS, dashboard presence) without the
-- rendering layer needing a special case for admin marks. The
-- UNIQUE(member_id, date) constraint guarantees idempotency: a
-- second mark for the same (member, date) returns SQLITE_CONSTRAINT
-- to the caller, which the service layer translates into
-- "already marked".
INSERT INTO wfh_requests (id, member_id, date, status, is_admin_marked, marked_by, marked_at)
VALUES (?, ?, ?, 'approved', 1, ?, ?);

-- name: IsAdminMarkedWFH :one
-- Returns 1 if an admin-marked row exists for (member_id, date),
-- 0 otherwise. Cheap point-lookup behind the
-- idx_wfh_requests_admin_marked index.
SELECT is_admin_marked
FROM wfh_requests
WHERE member_id = ? AND date = ?;

-- name: GetUpcomingWFHForMember :many
SELECT id, member_id, date, status, is_recurring, is_admin_marked, marked_by, marked_at, denial_reason, origin
FROM wfh_requests
WHERE member_id = ?
  AND date >= date('now')
  AND date <= date('now', '+' || ? || ' days')
  AND status = 'approved'
ORDER BY date;

-- name: CreateApprovedRecurringWFHRequest :execresult
-- origin='recurring' is set explicitly even though the column default
-- is 'ad_hoc': the migration 000024 backfilled historical rows by
-- origin = 'recurring' WHERE is_recurring = 1, and we want every new
-- recurring row to land with the matching origin so the picker,
-- quota counter, and calendar layers can branch on it without
-- inferring from is_recurring.
INSERT INTO wfh_requests (id, member_id, date, status, is_recurring, settled_at, origin)
VALUES (?, ?, ?, 'approved', 1, ?, 'recurring');

-- name: GetWFHRequestByID :one
SELECT id, member_id, date, status, created_at, settled_at, withdrawn_by, withdrawn_at, is_recurring, is_admin_marked, marked_by, marked_at, denial_reason, origin
FROM wfh_requests
WHERE id = ?;

-- name: GetWFHRequestByMemberAndDate :one
SELECT id, member_id, date, status, created_at, settled_at, withdrawn_by, withdrawn_at, is_recurring, is_admin_marked, marked_by, marked_at, denial_reason, origin
FROM wfh_requests
WHERE member_id = ? AND date = ?;

-- name: GetWFHRequestsByDate :many
SELECT id, member_id, date, status, created_at, settled_at, withdrawn_by, withdrawn_at, is_recurring, is_admin_marked, marked_by, marked_at, denial_reason, origin
FROM wfh_requests
WHERE date = ?
ORDER BY created_at ASC;

-- name: GetWFHRequestsByDateAndStatus :many
SELECT id, member_id, date, status, created_at, settled_at, withdrawn_by, withdrawn_at, is_recurring, is_admin_marked, marked_by, marked_at, denial_reason, origin
FROM wfh_requests
WHERE date = ? AND status = ?
ORDER BY created_at ASC;

-- name: GetWFHRequestsByMember :many
SELECT id, member_id, date, status, created_at, settled_at, withdrawn_by, withdrawn_at, is_recurring, is_admin_marked, marked_by, marked_at, denial_reason, origin
FROM wfh_requests
WHERE member_id = ?
ORDER BY date DESC;

-- name: GetWFHRequestsByMemberAndPeriod :many
SELECT id, member_id, date, status, created_at, settled_at, withdrawn_by, withdrawn_at, is_recurring, is_admin_marked, marked_by, marked_at, denial_reason, origin
FROM wfh_requests
WHERE member_id = ?
  AND date >= ?
  AND date <= ?
  AND status IN ('pending', 'approved')
ORDER BY date ASC;

-- name: GetPendingWFHRequestsForSettlement :many
SELECT id, member_id, date, status, created_at, settled_at, withdrawn_by, withdrawn_at, is_recurring, is_admin_marked, marked_by, marked_at, denial_reason, origin
FROM wfh_requests
WHERE status = 'pending'
  AND is_recurring = 0
  AND date <= ?
ORDER BY date ASC, created_at ASC;

-- name: GetAllWFHRequests :many
SELECT id, member_id, date, status, created_at, settled_at, withdrawn_by, withdrawn_at, is_recurring, is_admin_marked, marked_by, marked_at, denial_reason, origin
FROM wfh_requests
ORDER BY date DESC, created_at DESC;

-- name: UpdateWFHRequestStatus :execresult
UPDATE wfh_requests
SET status = ?, settled_at = ?
WHERE id = ?;

-- name: ResurrectWFHRequest :execresult
-- Flip a previously cancelled or self-withdrawn row back to pending and
-- clear the audit fields, so the user can change their mind and re-request
-- WFH for the same date. Only self-withdrawals are resurrectable: admin
-- withdrawals (withdrawn_by IS NOT NULL) are preserved as final decisions.
-- is_recurring is cleared on resurrect so the row is treated as ad-hoc:
-- settlement filters is_recurring=0 (so recurring rows are skipped), and
-- preserving the flag would leave the resurrected row stuck in pending
-- with neither settlement nor the materializer able to advance it.
UPDATE wfh_requests
SET status = 'pending',
    settled_at = NULL,
    withdrawn_by = NULL,
    withdrawn_at = NULL,
    is_recurring = 0
WHERE id = ?
  AND (
    status = 'cancelled'
    OR (status = 'withdrawn' AND withdrawn_by IS NULL)
  );

-- name: UpdateWFHRequestWithdrawn :execresult
UPDATE wfh_requests
SET status = 'withdrawn', withdrawn_by = ?, withdrawn_at = CURRENT_TIMESTAMP
WHERE id = ?;

-- name: DeleteWFHRequest :exec
DELETE FROM wfh_requests
WHERE id = ?;

-- name: CountApprovedWFHByDate :one
SELECT COUNT(*) AS count
FROM wfh_requests
WHERE date = ? AND status = 'approved';

-- name: CountWFHRequestsBefore :one
-- Count wfh_requests rows whose date is strictly before the cutoff.
-- Used by the past-period purge dry-run to preview the affected row
-- count without touching the table. The cutoff is the start of the
-- previous quota period: rows on or after that date are kept.
SELECT COUNT(*) AS count
FROM wfh_requests
WHERE date < ?;

-- name: PurgeWFHRequestsBefore :execresult
DELETE FROM wfh_requests WHERE date < ?;
-- Hard-delete wfh_requests rows whose date is strictly before the cutoff.
-- The cutoff is the start of the previous quota period: rows on or after
-- that date are kept so the current and previous periods remain visible.
-- Past-period cleanup is a no-recovery operation — callers should run the
-- matching CountWFHRequestsBefore first when they want to preview impact.

