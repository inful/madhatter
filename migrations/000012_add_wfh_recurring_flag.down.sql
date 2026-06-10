DROP INDEX IF EXISTS idx_wfh_requests_recurring;
ALTER TABLE wfh_requests DROP COLUMN is_recurring;
