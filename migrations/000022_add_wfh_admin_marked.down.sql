-- SQLite does not support DROP COLUMN for older tables; this is the
-- recommended pattern for rolling back ADD COLUMN migrations: rename
-- the existing table, rebuild it without the new columns, copy the
-- surviving rows back, and drop the renamed copy. The IF EXISTS
-- guard makes the script a no-op if the columns were never added.

DROP INDEX IF EXISTS idx_wfh_requests_admin_marked;

ALTER TABLE wfh_requests RENAME TO wfh_requests_old;

CREATE TABLE wfh_requests (
    id          TEXT PRIMARY KEY,
    member_id   TEXT NOT NULL,
    date        DATE NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending', 'approved', 'denied', 'cancelled', 'withdrawn')),
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    settled_at  DATETIME,
    withdrawn_by TEXT,
    withdrawn_at DATETIME,
    FOREIGN KEY (member_id)     REFERENCES team_members(id) ON DELETE CASCADE,
    FOREIGN KEY (withdrawn_by)  REFERENCES users(id)        ON DELETE SET NULL,
    UNIQUE(member_id, date)
);

INSERT INTO wfh_requests (
    id, member_id, date, status, created_at, settled_at, withdrawn_by, withdrawn_at
)
SELECT
    id, member_id, date, status, created_at, settled_at, withdrawn_by, withdrawn_at
FROM wfh_requests_old;

DROP TABLE wfh_requests_old;

CREATE INDEX IF NOT EXISTS idx_wfh_requests_date   ON wfh_requests(date);
CREATE INDEX IF NOT EXISTS idx_wfh_requests_member ON wfh_requests(member_id);
CREATE INDEX IF NOT EXISTS idx_wfh_requests_status ON wfh_requests(status);
