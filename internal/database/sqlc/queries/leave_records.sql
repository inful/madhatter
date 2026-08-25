-- name: CreateLeaveRecord :execresult
INSERT INTO leave_records (id, member_id, start_date, end_date, status, leave_type)
VALUES (?, ?, ?, ?, 'pending', ?);

-- name: GetLeaveByDate :many
SELECT id, member_id, start_date, end_date, cover_member_id, status, leave_type, created_at
FROM leave_records
WHERE ? >= start_date AND ? <= end_date AND status != 'completed';

-- name: UpdateLeaveStatus :exec
UPDATE leave_records
SET status = ?
WHERE id = ?;

-- name: GetLeaveByID :one
SELECT id, member_id, start_date, end_date, cover_member_id, status, leave_type, created_at
FROM leave_records
WHERE id = ?;

-- name: GetLeaveRecords :many
SELECT id, member_id, start_date, end_date, cover_member_id, status, leave_type, created_at
FROM leave_records
WHERE (status = ? OR ? = '')
ORDER BY start_date DESC;

-- name: UpdateLeaveCoverMember :exec
UPDATE leave_records
SET cover_member_id = ?
WHERE id = ?;

-- name: UpdateLeaveRecord :exec
UPDATE leave_records
SET member_id = ?, start_date = ?, end_date = ?, status = ?, leave_type = ?
WHERE id = ?;

-- name: DeleteLeaveRecord :exec
DELETE FROM leave_records
WHERE id = ?;

-- name: DeleteExpiredLeaveRecords :exec
DELETE FROM leave_records
WHERE end_date < ?;