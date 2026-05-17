-- Backfill is_swapped for swaps that were already accepted before the column existed.
UPDATE rota_assignments
SET is_swapped = 1
WHERE id IN (
    SELECT requester_assignment_id
    FROM hat_swaps
    WHERE status = 'accepted'

    UNION

    SELECT target_assignment_id
    FROM hat_swaps
    WHERE status = 'accepted'
);