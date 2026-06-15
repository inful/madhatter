-- name: GetR1RotationState :one
-- Returns the R1 (original-HAT) rotation state, or sql.ErrNoRows
-- if no R1 assignment has been written yet. The R1 rotation is
-- kept in its own table (r1_rotation_state) so its write rules
-- (only advance on successful assignment write) don't collide
-- with the cover rotation's write rules (advance on every
-- computation).
SELECT last_date, last_index
FROM r1_rotation_state
LIMIT 1;

-- name: UpsertR1RotationState :exec
-- Writes the R1 rotation state. The table holds at most one
-- row, so this either inserts the first row or replaces the
-- existing one. R1 is keyed by the (date, index) pair of the
-- most recently written original assignment.
INSERT OR REPLACE INTO r1_rotation_state (last_date, last_index)
VALUES (?, ?);
