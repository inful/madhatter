-- Rollback: Add type column back to leave_records table
-- Note: This will add the column with a default value since we can't restore the original data

-- SQLite doesn't support ADD COLUMN with NOT NULL directly, so we recreate the table
-- Step 1: Create new table with type column
CREATE TABLE IF NOT EXISTS leave_records_new (
    id TEXT PRIMARY KEY,
    member_id TEXT NOT NULL,
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    type TEXT NOT NULL DEFAULT 'annual',
    cover_member_id TEXT,
    status TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (member_id) REFERENCES team_members(id),
    FOREIGN KEY (cover_member_id) REFERENCES team_members(id)
);

-- Step 2: Copy data from current table (with default type value)
INSERT INTO leave_records_new (id, member_id, start_date, end_date, type, cover_member_id, status, created_at)
SELECT id, member_id, start_date, end_date, 'annual', cover_member_id, status, created_at
FROM leave_records;

-- Step 3: Drop current table
DROP TABLE leave_records;

-- Step 4: Rename new table to original name
ALTER TABLE leave_records_new RENAME TO leave_records;

-- Step 5: Recreate index
CREATE INDEX IF NOT EXISTS idx_leave_records_date ON leave_records(start_date, end_date);
