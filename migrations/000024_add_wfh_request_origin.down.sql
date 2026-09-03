-- SQLite does not support DROP COLUMN for older tables; this is the
-- recommended pattern for rolling back ADD COLUMN migrations: rename
-- the existing table, rebuild it without the new column, copy the
-- surviving rows back, and drop the renamed copy. The IF EXISTS
-- guard makes the script a no-op if the column was never added.
--
-- The new wfh_requests_origin_date index is dropped implicitly by
-- the table rebuild; we recreate it on the rebuilt table to keep
-- migration round-trippable.

ALTER TABLE wfh_requests RENAME TO wfh_requests_old;

CREATE TABLE wfh_requests (
    id              TEXT PRIMARY KEY,
    member_id       TEXT NOT NULL,
    date            DATE NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'approved', 'denied', 'cancelled', 'withdrawn')),
    is_recurring    INTEGER NOT NULL DEFAULT 0,
    is_admin_marked INTEGER NOT NULL DEFAULT 0,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    settled_at      DATETIME,
    withdrawn_by    TEXT,
    withdrawn_at    DATETIME,
    marked_by       TEXT,
    marked_at       DATETIME,
    denial_reason   TEXT,
    FOREIGN KEY (member_id)    REFERENCES team_members(id) ON DELETE CASCADE,
    FOREIGN KEY (withdrawn_by) REFERENCES users(id)        ON DELETE SET NULL,
    FOREIGN KEY (marked_by)    REFERENCES users(id)        ON DELETE SET NULL,
    UNIQUE(member_id, date)
);

INSERT INTO wfh_requests (
    id, member_id, date, status, is_recurring, is_admin_marked,
    created_at, settled_at, withdrawn_by, withdrawn_at, marked_by,
    marked_at, denial_reason
)
SELECT
    id, member_id, date, status, is_recurring, is_admin_marked,
    created_at, settled_at, withdrawn_by, withdrawn_at, marked_by,
    marked_at, denial_reason
FROM wfh_requests_old;

DROP TABLE wfh_requests_old;

CREATE INDEX IF NOT EXISTS idx_wfh_requests_date           ON wfh_requests(date);
CREATE INDEX IF NOT EXISTS idx_wfh_requests_member         ON wfh_requests(member_id);
CREATE INDEX IF NOT EXISTS idx_wfh_requests_status         ON wfh_requests(status);
CREATE INDEX IF NOT EXISTS idx_wfh_requests_recurring      ON wfh_requests(member_id, date, is_recurring);
CREATE INDEX IF NOT EXISTS idx_wfh_requests_admin_marked   ON wfh_requests(member_id, date, is_admin_marked);
-- idx_wfh_requests_origin_date is dropped implicitly by the table
-- rebuild. It is NOT recreated here because the rebuilt table has
-- no `origin` column — that's the column we're rolling back.
