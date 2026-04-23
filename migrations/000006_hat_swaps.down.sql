-- Drop hat_swaps table and its indexes.
DROP TRIGGER IF EXISTS trg_hat_swaps_pending_update;
DROP TRIGGER IF EXISTS trg_hat_swaps_pending_insert;
DROP INDEX IF EXISTS idx_hat_swaps_target_assignment;
DROP INDEX IF EXISTS idx_hat_swaps_requester_assignment;
DROP INDEX IF EXISTS idx_hat_swaps_target;
DROP INDEX IF EXISTS idx_hat_swaps_requester;
DROP TABLE IF EXISTS hat_swaps;
