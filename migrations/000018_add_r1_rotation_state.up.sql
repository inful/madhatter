-- Add a separate table for the R1 (original-HAT) rotation state.
-- R1 was previously sharing the cover_rotation_state table, but
-- the two rotations have different write semantics: R2 advances
-- on every cover-index computation, R1 advances only when a
-- new original assignment is written. Sharing a row meant the
-- first R1 write created a row with default cover values, which
-- then corrupted R2's index computation. Splitting them keeps
-- the two rotations fully independent.
CREATE TABLE IF NOT EXISTS r1_rotation_state (
    last_date DATE NOT NULL,
    last_index INTEGER NOT NULL
);
