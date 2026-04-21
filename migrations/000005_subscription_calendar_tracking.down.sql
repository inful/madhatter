-- Remove last_used_rota_at and last_used_meetings_at columns.
-- SQLite requires table recreation to remove columns.
PRAGMA foreign_keys = OFF;

CREATE TABLE calendar_subscriptions_new (
    id TEXT PRIMARY KEY,
    member_id TEXT NOT NULL,
    token TEXT UNIQUE NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_used_at DATETIME,
    FOREIGN KEY (member_id) REFERENCES team_members(id) ON DELETE CASCADE
);

INSERT INTO calendar_subscriptions_new (id, member_id, token, created_at, last_used_at)
    SELECT id, member_id, token, created_at, last_used_at FROM calendar_subscriptions;

DROP TABLE calendar_subscriptions;

ALTER TABLE calendar_subscriptions_new RENAME TO calendar_subscriptions;

PRAGMA foreign_keys = ON;
