-- Remove last_used_at column from calendar_subscriptions.
-- SQLite does not support DROP COLUMN before version 3.35; recreate the table.
PRAGMA foreign_keys = OFF;

CREATE TABLE calendar_subscriptions_new (
    id TEXT PRIMARY KEY,
    member_id TEXT NOT NULL,
    token TEXT UNIQUE NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (member_id) REFERENCES team_members(id) ON DELETE CASCADE
);

INSERT INTO calendar_subscriptions_new (id, member_id, token, created_at)
    SELECT id, member_id, token, created_at FROM calendar_subscriptions;

DROP TABLE calendar_subscriptions;

ALTER TABLE calendar_subscriptions_new RENAME TO calendar_subscriptions;

PRAGMA foreign_keys = ON;
