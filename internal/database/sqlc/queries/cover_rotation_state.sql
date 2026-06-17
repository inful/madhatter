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

-- name: GetReassignmentAnchor :one
-- Reads the ReassignCovers-only anchor. Returns sql.ErrNoRows if the
-- row has never been written, which signals a fresh database.
SELECT last_reassign_date, last_reassign_index
FROM cover_rotation_state
WHERE id = 1;

-- name: EnsureReassignmentAnchorRow :exec
-- INSERT-OR-IGNORE the single row of cover_rotation_state. Used by
-- the reassign path so its subsequent UPDATE does not fail on a
-- fresh database where the row does not exist yet. Only the id is
-- inserted; the ad-hoc columns stay NULL (or whatever a prior
-- ad-hoc HandleLeaveChange left them as) and the reassign columns
-- are populated by the subsequent UPDATE.
INSERT OR IGNORE INTO cover_rotation_state (id) VALUES (1);

-- name: UpdateReassignmentAnchor :exec
-- Updates the reassign anchor columns only. Does not touch the
-- ad-hoc last_date / last_index columns, so a concurrent ad-hoc
-- HandleLeaveChange is safe. The reassign path never reads or
-- writes the ad-hoc state.
UPDATE cover_rotation_state
SET last_reassign_date = ?, last_reassign_index = ?
WHERE id = 1;
