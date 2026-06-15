-- Tracks which version of the cover-assignment algorithm has been applied
-- to the rota. The periodic cover-reassignment runner (ReassignCovers) uses
-- this as the "last applied" anchor: if applied_version < the binary's
-- CoverAlgorithmVersion constant, the runner is invoked so existing cover
-- assignments converge to the current algorithm's output.
--
-- Single-row table. last_run_at and last_run_changed are diagnostic — they
-- record when the runner last fired and how many covers it actually changed,
-- which is useful for confirming a no-op rerun or spotting a regression.
CREATE TABLE IF NOT EXISTS cover_algorithm_state (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    applied_version INTEGER NOT NULL DEFAULT 0,
    last_run_at DATETIME,
    last_run_changed INTEGER NOT NULL DEFAULT 0
);
INSERT OR IGNORE INTO cover_algorithm_state (id, applied_version) VALUES (1, 0);
