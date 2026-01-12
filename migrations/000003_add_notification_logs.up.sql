CREATE TABLE IF NOT EXISTS notification_logs (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    date DATE NOT NULL,
    member_id TEXT,
    assignment_id TEXT,
    message TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (member_id) REFERENCES team_members(id) ON DELETE SET NULL,
    FOREIGN KEY (assignment_id) REFERENCES rota_assignments(id) ON DELETE SET NULL,
    UNIQUE(kind, date)
);

CREATE INDEX IF NOT EXISTS idx_notification_logs_kind_date ON notification_logs(kind, date);
