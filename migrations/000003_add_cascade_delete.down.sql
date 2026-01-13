-- Revert CASCADE delete behavior from foreign keys
-- SQLite requires recreating tables to modify foreign keys

PRAGMA foreign_keys = OFF;

-- Recreate leave_records table without CASCADE
CREATE TABLE leave_records_new (
    id TEXT PRIMARY KEY,
    member_id TEXT NOT NULL,
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    cover_member_id TEXT,
    status TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (member_id) REFERENCES team_members(id),
    FOREIGN KEY (cover_member_id) REFERENCES team_members(id)
);

-- Copy data
INSERT INTO leave_records_new SELECT * FROM leave_records;

-- Drop old table
DROP TABLE leave_records;

-- Rename new table
ALTER TABLE leave_records_new RENAME TO leave_records;

-- Recreate index
CREATE INDEX IF NOT EXISTS idx_leave_records_date ON leave_records(start_date, end_date);

-- Recreate rota_assignments table without CASCADE
CREATE TABLE rota_assignments_new (
    id TEXT PRIMARY KEY,
    date DATE NOT NULL,
    member_id TEXT NOT NULL,
    is_cover INTEGER DEFAULT 0,
    original_assignment_id TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (member_id) REFERENCES team_members(id),
    FOREIGN KEY (original_assignment_id) REFERENCES rota_assignments(id),
    UNIQUE(date, is_cover)
);

-- Copy data
INSERT INTO rota_assignments_new SELECT * FROM rota_assignments;

-- Drop old table
DROP TABLE rota_assignments;

-- Rename new table
ALTER TABLE rota_assignments_new RENAME TO rota_assignments;

-- Recreate indexes
CREATE INDEX IF NOT EXISTS idx_rota_assignments_date ON rota_assignments(date);
CREATE INDEX IF NOT EXISTS idx_rota_assignments_member ON rota_assignments(member_id);

-- Recreate calendar_subscriptions table without CASCADE
CREATE TABLE calendar_subscriptions_new (
    id TEXT PRIMARY KEY,
    member_id TEXT NOT NULL,
    token TEXT UNIQUE NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (member_id) REFERENCES team_members(id)
);

-- Copy data
INSERT INTO calendar_subscriptions_new SELECT * FROM calendar_subscriptions;

-- Drop old table
DROP TABLE calendar_subscriptions;

-- Rename new table
ALTER TABLE calendar_subscriptions_new RENAME TO calendar_subscriptions;

-- Recreate index
CREATE INDEX IF NOT EXISTS idx_calendar_subscriptions_token ON calendar_subscriptions(token);

PRAGMA foreign_keys = ON;
