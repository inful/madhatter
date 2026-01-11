-- name: CreateLeaveRecord :execresult
INSERT INTO leave_records (id, member_id, start_date, end_date, status)
VALUES (?, ?, ?, ?, 'pending');

-- name: GetLeaveByDate :many
SELECT id, member_id, start_date, end_date, cover_member_id, status, created_at
FROM leave_records
WHERE ? >= start_date AND ? <= end_date AND status != 'completed';

-- name: UpdateLeaveStatus :exec
UPDATE leave_records
SET status = ?
WHERE id = ?;

-- name: GetLeaveByID :one
SELECT id, member_id, start_date, end_date, cover_member_id, status, created_at
FROM leave_records
WHERE id = ?;

-- name: GetLeaveRecords :many
SELECT id, member_id, start_date, end_date, cover_member_id, status, created_at
FROM leave_records
WHERE (status = ? OR ? = '')
ORDER BY start_date DESC;

-- name: UpdateLeaveCoverMember :exec
UPDATE leave_records
SET cover_member_id = ?
WHERE id = ?;