-- Add an `origin` column to wfh_requests so the picker, quota, API,
-- and calendar layers can distinguish a self-requested WFH from a
-- system-assigned one, a recurring one, or a swap-target one.
--
-- Default 'ad_hoc' matches every existing row's intent — they were
-- all self-requested. The UPDATE below backfills 'recurring' for
-- rows that the recurring materializer inserted, so the picker and
-- quota queries see the right origin from the moment the migration
-- lands (no need for a separate "is the origin column accurate?"
-- backfill pass).
--
-- The covering composite index `idx_wfh_requests_origin_date`
-- keeps quota queries (which filter by origin + period) and the
-- picker's candidate scan O(period) instead of full-table scans
-- on history.
ALTER TABLE wfh_requests ADD COLUMN origin TEXT NOT NULL DEFAULT 'ad_hoc'
    CHECK (origin IN ('ad_hoc', 'recurring', 'assigned', 'swap'));

UPDATE wfh_requests SET origin = 'recurring' WHERE is_recurring = 1;

CREATE INDEX IF NOT EXISTS idx_wfh_requests_origin_date
    ON wfh_requests(origin, date);
