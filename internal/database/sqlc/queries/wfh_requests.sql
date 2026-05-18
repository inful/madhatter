-- name: CreateWFHRequest :execresult
INSERT INTO wfh_requests (id, member_id, date, status)
VALUES (?, ?, ?, 'pending');

-- name: GetWFHRequestByID :one
SELECT id, member_id, date, status, created_at, settled_at, withdrawn_by, withdrawn_at
FROM wfh_requests
WHERE id = ?;

-- name: GetWFHRequestByMemberAndDate :one
SELECT id, member_id, date, status, created_at, settled_at, withdrawn_by, withdrawn_at
FROM wfh_requests
WHERE member_id = ? AND date = ?;

-- name: GetWFHRequestsByDate :many
SELECT id, member_id, date, status, created_at, settled_at, withdrawn_by, withdrawn_at
FROM wfh_requests
WHERE date = ?
ORDER BY created_at ASC;

-- name: GetWFHRequestsByDateAndStatus :many
SELECT id, member_id, date, status, created_at, settled_at, withdrawn_by, withdrawn_at
FROM wfh_requests
WHERE date = ? AND status = ?
ORDER BY created_at ASC;

-- name: GetWFHRequestsByMember :many
SELECT id, member_id, date, status, created_at, settled_at, withdrawn_by, withdrawn_at
FROM wfh_requests
WHERE member_id = ?
ORDER BY date DESC;

-- name: GetWFHRequestsByMemberAndPeriod :many
SELECT id, member_id, date, status, created_at, settled_at, withdrawn_by, withdrawn_at
FROM wfh_requests
WHERE member_id = ?
  AND date >= ?
  AND date <= ?
  AND status IN ('pending', 'approved')
ORDER BY date ASC;

-- name: GetPendingWFHRequestsForSettlement :many
SELECT id, member_id, date, status, created_at, settled_at, withdrawn_by, withdrawn_at
FROM wfh_requests
WHERE status = 'pending'
  AND date <= ?
ORDER BY date ASC, created_at ASC;

-- name: GetAllWFHRequests :many
SELECT id, member_id, date, status, created_at, settled_at, withdrawn_by, withdrawn_at
FROM wfh_requests
ORDER BY date DESC, created_at DESC;

-- name: UpdateWFHRequestStatus :execresult
UPDATE wfh_requests
SET status = ?, settled_at = ?
WHERE id = ?;

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
