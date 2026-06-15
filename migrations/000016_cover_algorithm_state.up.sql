-- No-op migration retained for backward compatibility with databases
-- that were brought up under the previous (version-counter-based)
-- implementation. New databases apply this as a no-op; existing
-- databases see the migration is present and skip the redundant
-- run. The cover_algorithm_state table is no longer used.
SELECT 1;
