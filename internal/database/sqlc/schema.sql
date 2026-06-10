-- Support Rota Database Schema
-- This schema is used by sqlc to generate type-safe Go code

-- Enable foreign keys (sqlc will handle this, but documented for reference)
-- PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS team_members (
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

CREATE TABLE IF NOT EXISTS leave_records (
    id TEXT PRIMARY KEY,
    member_id TEXT NOT NULL,
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    cover_member_id TEXT,
    status TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (member_id) REFERENCES team_members(id) ON DELETE CASCADE,
    FOREIGN KEY (cover_member_id) REFERENCES team_members(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS rota_assignments (
    id TEXT PRIMARY KEY,
    date DATE NOT NULL,
    member_id TEXT NOT NULL,
    is_cover INTEGER DEFAULT 0,
    original_assignment_id TEXT,
    is_swapped INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (member_id) REFERENCES team_members(id) ON DELETE CASCADE,
    FOREIGN KEY (original_assignment_id) REFERENCES rota_assignments(id) ON DELETE SET NULL,
    UNIQUE(date, is_cover)
);

CREATE TABLE IF NOT EXISTS calendar_subscriptions (
    id TEXT PRIMARY KEY,
    member_id TEXT NOT NULL,
    token TEXT UNIQUE NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_used_at DATETIME,
    last_used_rota_at DATETIME,
    last_used_meetings_at DATETIME,
    FOREIGN KEY (member_id) REFERENCES team_members(id) ON DELETE CASCADE
);

-- OAuth2 Authentication Tables
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    email TEXT UNIQUE NOT NULL,
    name TEXT NOT NULL,
    provider TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    is_admin INTEGER DEFAULT 0,
    is_active INTEGER DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(provider, provider_id)
);

CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    token TEXT UNIQUE NOT NULL,
    expires_at DATETIME NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS oauth_tokens (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    provider TEXT NOT NULL,
    access_token TEXT NOT NULL,
    refresh_token TEXT,
    token_type TEXT,
    expires_at DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    UNIQUE(user_id, provider)
);

-- Indexes for better query performance
CREATE INDEX IF NOT EXISTS idx_leave_records_date ON leave_records(start_date, end_date);
CREATE INDEX IF NOT EXISTS idx_rota_assignments_date ON rota_assignments(date);
CREATE INDEX IF NOT EXISTS idx_rota_assignments_member ON rota_assignments(member_id);
CREATE INDEX IF NOT EXISTS idx_calendar_subscriptions_token ON calendar_subscriptions(token);
CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
CREATE INDEX IF NOT EXISTS idx_users_provider ON users(provider, provider_id);
CREATE INDEX IF NOT EXISTS idx_sessions_token ON sessions(token);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);
CREATE INDEX IF NOT EXISTS idx_oauth_tokens_user_provider ON oauth_tokens(user_id, provider);

-- API Token Authentication Tables
CREATE TABLE IF NOT EXISTS api_tokens (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    name TEXT NOT NULL,
    token_hash TEXT UNIQUE NOT NULL,
    is_active INTEGER DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME,
    last_used_at DATETIME,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_api_tokens_user ON api_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_api_tokens_token_hash ON api_tokens(token_hash);
CREATE INDEX IF NOT EXISTS idx_api_tokens_active ON api_tokens(is_active);

-- HAT Day Swap Requests
CREATE TABLE IF NOT EXISTS hat_swaps (
    id                      TEXT PRIMARY KEY,
    requester_assignment_id TEXT NOT NULL,
    target_assignment_id    TEXT NOT NULL,
    requester_member_id     TEXT NOT NULL,
    target_member_id        TEXT NOT NULL,
    status                  TEXT NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending', 'accepted', 'rejected', 'cancelled')),
    created_at              DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at              DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (requester_assignment_id) REFERENCES rota_assignments(id) ON DELETE CASCADE,
    FOREIGN KEY (target_assignment_id)    REFERENCES rota_assignments(id) ON DELETE CASCADE,
    FOREIGN KEY (requester_member_id)     REFERENCES team_members(id) ON DELETE CASCADE,
    FOREIGN KEY (target_member_id)        REFERENCES team_members(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_hat_swaps_requester ON hat_swaps(requester_member_id, status);
CREATE INDEX IF NOT EXISTS idx_hat_swaps_target    ON hat_swaps(target_member_id, status);
CREATE INDEX IF NOT EXISTS idx_hat_swaps_requester_assignment ON hat_swaps(requester_assignment_id, status);
CREATE INDEX IF NOT EXISTS idx_hat_swaps_target_assignment    ON hat_swaps(target_assignment_id, status);

CREATE TRIGGER IF NOT EXISTS trg_hat_swaps_pending_insert
BEFORE INSERT ON hat_swaps
WHEN NEW.status = 'pending'
BEGIN
    SELECT RAISE(FAIL, 'pending swap already exists for one of the assignments')
    WHERE EXISTS (
        SELECT 1
        FROM hat_swaps
        WHERE status = 'pending'
          AND (
              requester_assignment_id IN (NEW.requester_assignment_id, NEW.target_assignment_id)
              OR target_assignment_id IN (NEW.requester_assignment_id, NEW.target_assignment_id)
          )
    );
END;

CREATE TRIGGER IF NOT EXISTS trg_hat_swaps_pending_update
BEFORE UPDATE OF requester_assignment_id, target_assignment_id, status ON hat_swaps
WHEN NEW.status = 'pending'
BEGIN
    SELECT RAISE(FAIL, 'pending swap already exists for one of the assignments')
    WHERE EXISTS (
        SELECT 1
        FROM hat_swaps
        WHERE id <> NEW.id
          AND status = 'pending'
          AND (
              requester_assignment_id IN (NEW.requester_assignment_id, NEW.target_assignment_id)
              OR target_assignment_id IN (NEW.requester_assignment_id, NEW.target_assignment_id)
          )
    );
END;

-- Work-From-Home Requests
CREATE TABLE IF NOT EXISTS wfh_requests (
    id           TEXT PRIMARY KEY,
    member_id    TEXT NOT NULL,
    date         DATE NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'approved', 'denied', 'cancelled', 'withdrawn')),
    is_recurring INTEGER NOT NULL DEFAULT 0,
    created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
    settled_at   DATETIME,
    withdrawn_by TEXT,
    withdrawn_at DATETIME,
    FOREIGN KEY (member_id)    REFERENCES team_members(id) ON DELETE CASCADE,
    FOREIGN KEY (withdrawn_by) REFERENCES users(id)        ON DELETE SET NULL,
    UNIQUE(member_id, date)
);

CREATE INDEX IF NOT EXISTS idx_wfh_requests_date      ON wfh_requests(date);
CREATE INDEX IF NOT EXISTS idx_wfh_requests_member    ON wfh_requests(member_id);
CREATE INDEX IF NOT EXISTS idx_wfh_requests_status    ON wfh_requests(status);
CREATE INDEX IF NOT EXISTS idx_wfh_requests_recurring ON wfh_requests(member_id, date, is_recurring);

-- Notification Outbox
CREATE TABLE IF NOT EXISTS notification_outbox (
    id              TEXT PRIMARY KEY,
    event_kind      TEXT NOT NULL,
    channel         TEXT NOT NULL,
    recipient       TEXT NOT NULL,
    recipient_name  TEXT,
    subject         TEXT NOT NULL,
    body            TEXT NOT NULL,
    attempts        INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT,
    next_attempt_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'sent', 'dead')),
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    sent_at         DATETIME
);

CREATE INDEX IF NOT EXISTS idx_outbox_pending_due
    ON notification_outbox(status, next_attempt_at)
    WHERE status = 'pending';

-- Notification Preferences (per-member unsubscribe)
CREATE TABLE IF NOT EXISTS notification_preferences (
    member_id       TEXT PRIMARY KEY,
    email_enabled   INTEGER NOT NULL DEFAULT 1,
    disabled_at     DATETIME,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (member_id) REFERENCES team_members(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_notification_preferences_disabled
    ON notification_preferences(email_enabled)
    WHERE email_enabled = 0;