-- Drop in reverse-FK order. The wfh_assignment_swaps table has
-- FK dependents on wfh_requests (the assigned row) and
-- team_members (the target member); CASCADE on the parent
-- tables' deletes cleans any dependents automatically, but for
-- an explicit rollback we drop this table directly. No other
-- table in the migration chain references wfh_assignment_swaps.
DROP TABLE IF EXISTS wfh_assignment_swaps;
