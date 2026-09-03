-- name: CreateSwap :execresult
-- Insert a new pending swap request. Caller is responsible
-- for the 409-conflict check (any pending swap for the same
-- requester_wfh_request_id); the application enforces it
-- before calling this query.
INSERT INTO wfh_assignment_swaps
    (id, requester_wfh_request_id, target_member_id, swap_date, status)
VALUES (?, ?, ?, ?, 'pending');

-- name: GetSwapByID :one
SELECT id, requester_wfh_request_id, target_member_id, swap_date, status,
       created_at, updated_at, resolved_at
FROM wfh_assignment_swaps
WHERE id = ?;

-- name: GetPendingSwapsForTarget :many
-- Inbox read for the target member. Returns all swaps where
-- the target is the current user and status='pending'. The
-- idx_wfh_assignment_swaps_target index covers this.
SELECT id, requester_wfh_request_id, target_member_id, swap_date, status,
       created_at, updated_at, resolved_at
FROM wfh_assignment_swaps
WHERE target_member_id = ?
  AND status = 'pending'
ORDER BY created_at DESC;

-- name: GetPendingSwapsForRequester :many
-- Outbound read for the requester. Returns all swaps the
-- current member has opened that are still pending. Used
-- by the swap form to show "your swap is awaiting N's
-- decision" and by the cancel button.
SELECT id, requester_wfh_request_id, target_member_id, swap_date, status,
       created_at, updated_at, resolved_at
FROM wfh_assignment_swaps
WHERE requester_wfh_request_id IN
    (SELECT id FROM wfh_requests WHERE member_id = ?)
  AND status = 'pending'
ORDER BY created_at DESC;

-- name: GetPendingSwapForRequesterRow :one
-- 409-conflict guard: returns the pending swap for a given
-- assigned wfh_request row, if any. nil when no pending
-- swap exists. Used in handleWFHSwapCreate before the
-- INSERT to enforce "one pending swap per assigned row".
SELECT id, requester_wfh_request_id, target_member_id, swap_date, status,
       created_at, updated_at, resolved_at
FROM wfh_assignment_swaps
WHERE requester_wfh_request_id = ?
  AND status = 'pending';

-- name: UpdateSwapStatus :execresult
-- State transition for accept / reject / cancel. Caller
-- passes the new status string and a resolved_at
-- timestamp. status='cancelled' is used by both the requester
-- (voluntary) and the scheduler (auto-cancel because the
-- date passed).
UPDATE wfh_assignment_swaps
SET status = ?, updated_at = CURRENT_TIMESTAMP, resolved_at = ?
WHERE id = ?;

-- name: CancelExpiredSwaps :execresult
-- Auto-cancel pass run by SettlePendingRequests (step 15
-- of plans/assigned-wfh-plan.md): flips every pending swap
-- whose swap_date is strictly before today to status=
-- 'cancelled'. The idx_wfh_assignment_swaps_date index
-- covers this; the cutoff is computed in Go as today.
UPDATE wfh_assignment_swaps
SET status = 'cancelled',
    updated_at = CURRENT_TIMESTAMP,
    resolved_at = CURRENT_TIMESTAMP
WHERE status = 'pending'
  AND swap_date < ?;
