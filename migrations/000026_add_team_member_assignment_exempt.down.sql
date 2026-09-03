-- SQLite does not support DROP COLUMN for older tables; this is the
-- standard rename-and-rebuild pattern. The rebuilt table matches
-- the schema at v25 — every column except is_exempt_from_assignment
-- preserved.

ALTER TABLE team_members RENAME TO team_members_old;

CREATE TABLE team_members (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    email TEXT UNIQUE NOT NULL,
    is_active INTEGER DEFAULT 1,
    is_permanent_wfh INTEGER NOT NULL DEFAULT 0,
    recurring_wfh_monday INTEGER NOT NULL DEFAULT 0,
    recurring_wfh_tuesday INTEGER NOT NULL DEFAULT 0,
    recurring_wfh_wednesday INTEGER NOT NULL DEFAULT 0,
    recurring_wfh_thursday INTEGER NOT NULL DEFAULT 0,
    recurring_wfh_friday INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO team_members (
    id, name, email, is_active, is_permanent_wfh,
    recurring_wfh_monday, recurring_wfh_tuesday, recurring_wfh_wednesday,
    recurring_wfh_thursday, recurring_wfh_friday, created_at
)
SELECT
    id, name, email, is_active, is_permanent_wfh,
    recurring_wfh_monday, recurring_wfh_tuesday, recurring_wfh_wednesday,
    recurring_wfh_thursday, recurring_wfh_friday, created_at
FROM team_members_old;

DROP TABLE team_members_old;
