-- Revert migration 000028: drop the self-swap-rejection triggers.
-- Also leaves the cleanup of existing self-swap rows in place —
-- that's the correct historical state (a self-swap is a no-op by
-- definition; cancelling is the only safe resolution).

DROP TRIGGER IF EXISTS trg_hat_swaps_no_self_swap_insert;
DROP TRIGGER IF EXISTS trg_hat_swaps_no_self_swap_update;
