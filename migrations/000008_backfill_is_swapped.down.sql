-- Revert the backfill by clearing the flag from assignments linked to accepted swaps.
UPDATE rota_assignments
SET is_swapped = 0
WHERE id IN (
    SELECT requester_assignment_id
    FROM hat_swaps
    WHERE status = 'accepted'

    UNION

    SELECT target_assignment_id
    FROM hat_swaps
    WHERE status = 'accepted'
);