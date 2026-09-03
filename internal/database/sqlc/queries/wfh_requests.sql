-- name: DenyWFHRequest :execresult
UPDATE wfh_requests SET status = 'denied', settled_at = ?, denial_reason = ? WHERE id = ?;
-- Mark a request as denied and record the human-readable reason in
-- wfh_requests.denial_reason. The reason rides the same row to
-- the dashboard, the WFH list page, the admin manage page, and
-- the email notification so the user is never left guessing why their
-- request was rejected.
-- name: CreateWFHRequest :execresult
INSERT INTO wfh_requests (id, member_id, date, status)
VALUES (?, ?, ?, 'pending');

-- name: MarkAdminWFH :execresult
INSERT INTO wfh_requests (id, member_id, date, status, is_admin_marked, marked_by, marked_at)
VALUES (?, ?, ?, 'approved', 1, ?, ?);

-- name: IsAdminMarkedWFH :one
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
INSERT INTO wfh_requests (id, member_id, date, status, is_recurring, settled_at, origin)
VALUES (?, ?, ?, 'approved', 1, ?, 'recurring');

-- name: CreateApprovedAssignedWFHRequest :execresult
INSERT INTO wfh_requests (id, member_id, date, status, is_recurring, settled_at, origin)
VALUES (?, ?, ?, 'approved', 0, ?, 'assigned');

-- name: GetWFHRequestByMemberAndDate :one
SELECT id, member_id, date, status, created_at, settled_at, withdrawn_by, withdrawn_at, is_recurring, is_admin_marked, marked_by, marked_at, denial_reason, origin
FROM wfh_requests
WHERE member_id = ? AND date = ?;

-- name: GetWFHRequestByID :one
SELECT id, member_id, date, status, created_at, settled_at, withdrawn_by, withdrawn_at, is_recurring, is_admin_marked, marked_by, marked_at, denial_reason, origin
FROM wfh_requests
WHERE id = ?;

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

-- name: GetWFHRequestsVoluntaryInPeriod :many
SELECT id, member_id, date, status, created_at, settled_at, withdrawn_by, withdrawn_at, is_recurring, is_admin_marked, marked_by, marked_at, denial_reason, origin
FROM wfh_requests
WHERE member_id = ?
  AND date >= ?
  AND date <= ?
  AND status IN ('pending', 'approved')
  AND origin != 'assigned'
ORDER BY date ASC;

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
SELECT COUNT(*) AS count
FROM wfh_requests
WHERE date < ?;

-- name: PurgeWFHRequestsBefore :execresult
DELETE FROM wfh_requests WHERE date < ?;
