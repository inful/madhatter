-- Remove type column from leave_records table
-- This column was removed in commit 4af7328 but existing databases still have it

-- SQLite doesn't support DROP COLUMN directly, so we need to recreate the table
-- Step 1: Create new table without type column
CREATE TABLE IF NOT EXISTS leave_records_new (
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

-- Step 2: Copy data from old table to new table (excluding type column)
INSERT INTO leave_records_new (id, member_id, start_date, end_date, cover_member_id, status, created_at)
SELECT id, member_id, start_date, end_date, cover_member_id, status, created_at
FROM leave_records;

-- Step 3: Drop old table
DROP TABLE leave_records;

-- Step 4: Rename new table to original name
ALTER TABLE leave_records_new RENAME TO leave_records;

-- Step 5: Recreate index
CREATE INDEX IF NOT EXISTS idx_leave_records_date ON leave_records(start_date, end_date);
