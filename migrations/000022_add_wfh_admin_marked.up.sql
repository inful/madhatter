ALTER TABLE wfh_requests ADD COLUMN is_admin_marked INTEGER NOT NULL DEFAULT 0;
ALTER TABLE wfh_requests ADD COLUMN marked_by      TEXT    REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE wfh_requests ADD COLUMN marked_at      DATETIME;

CREATE INDEX IF NOT EXISTS idx_wfh_requests_admin_marked
    ON wfh_requests(member_id, date, is_admin_marked);
