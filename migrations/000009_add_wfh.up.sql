CREATE TABLE IF NOT EXISTS wfh_requests (
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

CREATE INDEX IF NOT EXISTS idx_wfh_requests_date   ON wfh_requests(date);
CREATE INDEX IF NOT EXISTS idx_wfh_requests_member ON wfh_requests(member_id);
CREATE INDEX IF NOT EXISTS idx_wfh_requests_status ON wfh_requests(status);
