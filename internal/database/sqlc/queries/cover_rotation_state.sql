-- name: GetCoverRotationState :one
-- Returns the single row of the cover_rotation_state table, or
-- sql.ErrNoRows if no state has been written yet (a fresh database
-- has no row until the first cover is computed).
SELECT last_date, last_index
FROM cover_rotation_state
WHERE id = 1;

-- name: UpsertCoverRotationState :exec
-- Inserts the cover rotation state row if it doesn't exist, otherwise
-- updates it in place. The table is constrained to a single row by
-- the CHECK (id = 1) clause, so this is the only way to write it.
INSERT INTO cover_rotation_state (id, last_date, last_index)
VALUES (1, ?, ?)
ON CONFLICT (id) DO UPDATE SET
    last_date = excluded.last_date,
    last_index = excluded.last_index;
