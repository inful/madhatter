-- Support Rota Database Schema
-- This schema is used by sqlc to generate type-safe Go code

-- Enable foreign keys (sqlc will handle this, but documented for reference)
-- PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS team_members (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    email TEXT UNIQUE NOT NULL,
    is_active INTEGER DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS leave_records (
    id TEXT PRIMARY KEY,
    member_id TEXT NOT NULL,
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    type TEXT NOT NULL,
    cover_member_id TEXT,
    status TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (member_id) REFERENCES team_members(id),
    FOREIGN KEY (cover_member_id) REFERENCES team_members(id)
);

CREATE TABLE IF NOT EXISTS rota_assignments (
    id TEXT PRIMARY KEY,
    date DATE NOT NULL,
    member_id TEXT NOT NULL,
    is_cover INTEGER DEFAULT 0,
    original_assignment_id TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (member_id) REFERENCES team_members(id),
    FOREIGN KEY (original_assignment_id) REFERENCES rota_assignments(id),
    UNIQUE(date, member_id)
);

CREATE TABLE IF NOT EXISTS calendar_subscriptions (
    id TEXT PRIMARY KEY,
    member_id TEXT NOT NULL,
    token TEXT UNIQUE NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (member_id) REFERENCES team_members(id)
);

-- Indexes for better query performance
CREATE INDEX IF NOT EXISTS idx_leave_records_date ON leave_records(start_date, end_date);
CREATE INDEX IF NOT EXISTS idx_rota_assignments_date ON rota_assignments(date);
CREATE INDEX IF NOT EXISTS idx_rota_assignments_member ON rota_assignments(member_id);
CREATE INDEX IF NOT EXISTS idx_calendar_subscriptions_token ON calendar_subscriptions(token);