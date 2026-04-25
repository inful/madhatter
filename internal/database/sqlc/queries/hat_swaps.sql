-- name: CreateHatSwap :execresult
INSERT INTO hat_swaps (
    id,
    requester_assignment_id,
    target_assignment_id,
    requester_member_id,
    target_member_id,
    status
) VALUES (?, ?, ?, ?, ?, 'pending');

-- name: GetHatSwapByID :one
SELECT id, requester_assignment_id, target_assignment_id,
       requester_member_id, target_member_id, status, created_at, updated_at
FROM hat_swaps
WHERE id = ?;

-- name: GetPendingSwapsForMember :many
SELECT id, requester_assignment_id, target_assignment_id,
       requester_member_id, target_member_id, status, created_at, updated_at
FROM hat_swaps
WHERE target_member_id = ? AND status = 'pending'
ORDER BY created_at DESC;

-- name: GetSwapsForMember :many
SELECT id, requester_assignment_id, target_assignment_id,
       requester_member_id, target_member_id, status, created_at, updated_at
FROM hat_swaps
WHERE requester_member_id = ? OR target_member_id = ?
ORDER BY created_at DESC;

-- name: GetOpenSwapForAssignment :one
SELECT id, requester_assignment_id, target_assignment_id,
       requester_member_id, target_member_id, status, created_at, updated_at
FROM hat_swaps
WHERE (requester_assignment_id = ? OR target_assignment_id = ?)
  AND status = 'pending'
LIMIT 1;

-- name: UpdateHatSwapStatus :execresult
UPDATE hat_swaps
SET status = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ? AND status = 'pending';

-- name: DeleteHatSwap :exec
DELETE FROM hat_swaps
WHERE id = ?;

-- name: CountPendingSwapsForMember :one
SELECT COUNT(*) AS count
FROM hat_swaps
WHERE target_member_id = ? AND status = 'pending';
