-- SQLite does not support DROP COLUMN in older versions, so we recreate the table without is_swapped.
CREATE TABLE rota_assignments_backup (
    id TEXT PRIMARY KEY,
    date DATE NOT NULL,
    member_id TEXT NOT NULL,
    is_cover INTEGER DEFAULT 0,
    original_assignment_id TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (member_id) REFERENCES team_members(id) ON DELETE CASCADE,
    FOREIGN KEY (original_assignment_id) REFERENCES rota_assignments_backup(id) ON DELETE SET NULL,
    UNIQUE(date, is_cover)
);

INSERT INTO rota_assignments_backup SELECT id, date, member_id, is_cover, original_assignment_id, created_at FROM rota_assignments;

DROP TABLE rota_assignments;

ALTER TABLE rota_assignments_backup RENAME TO rota_assignments;
