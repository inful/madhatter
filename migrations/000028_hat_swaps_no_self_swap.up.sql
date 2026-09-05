-- Reject self-swaps at the storage layer.
--
-- Bug fix for the v0.32.x anomaly: a self-swap row existed in the
-- production DB with status='accepted' and is_swapped=1 set on
-- both assignment rows, despite member_id never having actually
-- changed. The CreateHatSwap API correctly returns ErrSwapTargetSelf
-- when requesterMemberID == targetMemberID, so this row must have
-- been inserted via a non-API path (direct INSERT, migration data,
-- test fixture leak) — but we cannot rule out future bugs that
-- bypass the API check.
--
-- This migration closes the gap at the storage layer so the
-- impossible state is rejected by SQLite regardless of the path
-- that tries to insert it. A pair of BEFORE INSERT/UPDATE triggers
-- raise ABORT when requester_member_id == target_member_id. SQLite
-- 3.x's ALTER TABLE doesn't support adding table-level CHECK
-- constraints in a portable way, so we rely on the triggers as
-- the durable contract. (Defense in depth: the application-level
-- CreateHatSwap check at line 132 of hat_swaps.go is the primary
-- guard for the API path; the triggers catch any non-API path.)
--
-- We also clean up any existing self-swap rows: they cannot be
-- "fixed" by flipping member_ids (the swap is a no-op by definition),
-- so cancelling them is the only safe move. The associated
-- is_swapped=1 markers (set by migration 000008's backfill) are
-- reverted to 0 in the same transaction so the dashboard stops
-- showing a false "you swapped this" badge for those rows.

CREATE TRIGGER IF NOT EXISTS trg_hat_swaps_no_self_swap_insert
BEFORE INSERT ON hat_swaps
WHEN NEW.requester_member_id = NEW.target_member_id
BEGIN
    SELECT RAISE(ABORT, 'hat_swaps: requester_member_id and target_member_id must differ');
END;

CREATE TRIGGER IF NOT EXISTS trg_hat_swaps_no_self_swap_update
BEFORE UPDATE OF requester_member_id, target_member_id ON hat_swaps
WHEN NEW.requester_member_id = NEW.target_member_id
BEGIN
    SELECT RAISE(ABORT, 'hat_swaps: requester_member_id and target_member_id must differ');
END;

-- Cleanup: any existing self-swap rows get cancelled so the
-- dashboard stops showing them as live swaps. We deliberately
-- keep them in the table with status='cancelled' rather than
-- DELETE them, so audit/history still has them.
UPDATE hat_swaps
SET status = 'cancelled',
    updated_at = CURRENT_TIMESTAMP
WHERE requester_member_id = target_member_id
  AND status NOT IN ('cancelled', 'rejected');

-- The is_swapped=1 markers that migration 000008 set on these
-- cancelled swaps are wrong — the swap never actually moved
-- anyone. Reset them to 0.
UPDATE rota_assignments
SET is_swapped = 0
WHERE is_swapped = 1
  AND id IN (
      SELECT requester_assignment_id FROM hat_swaps
      WHERE status = 'cancelled'
        AND requester_member_id = target_member_id
      UNION
      SELECT target_assignment_id FROM hat_swaps
      WHERE status = 'cancelled'
        AND requester_member_id = target_member_id
  );
