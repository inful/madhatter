-- Adds is_recurring flag to wfh_requests so the materializer can mark rows
-- that were auto-approved from a member's recurring weekday contract.
-- Settlement queries filter on is_recurring=0 so contractual rows are
-- untouched by the on-site-minimums logic.
ALTER TABLE wfh_requests ADD COLUMN is_recurring INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_wfh_requests_recurring ON wfh_requests(member_id, date, is_recurring);
