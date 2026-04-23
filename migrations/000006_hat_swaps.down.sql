-- Drop hat_swaps table and its indexes.
DROP INDEX IF EXISTS idx_hat_swaps_target;
DROP INDEX IF EXISTS idx_hat_swaps_requester;
DROP TABLE IF EXISTS hat_swaps;
