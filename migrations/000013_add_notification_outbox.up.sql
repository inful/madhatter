CREATE TABLE notification_outbox (
    id             TEXT PRIMARY KEY,
    event_kind     TEXT NOT NULL,
    channel        TEXT NOT NULL,
    recipient      TEXT NOT NULL,
    recipient_name TEXT,
    subject        TEXT NOT NULL,
    body           TEXT NOT NULL,
    attempts       INTEGER NOT NULL DEFAULT 0,
    last_error     TEXT,
    next_attempt_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    status         TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'sent', 'dead')),
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    sent_at        DATETIME
);

CREATE INDEX idx_outbox_pending_due
    ON notification_outbox(status, next_attempt_at)
    WHERE status = 'pending';
