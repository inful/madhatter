-- SQLite < 3.35 doesn't support DROP COLUMN; rebuild the table
-- to remove unsubscribe_url. The down path mirrors what we
-- expect for an emergency rollback: any unsent rows are lost.
ALTER TABLE notification_outbox RENAME TO _notification_outbox_old;

CREATE TABLE notification_outbox (
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

INSERT INTO notification_outbox
    (id, event_kind, channel, recipient, recipient_name, subject, body,
     attempts, last_error, next_attempt_at, status, created_at, sent_at)
SELECT id, event_kind, channel, recipient, recipient_name, subject, body,
     attempts, last_error, next_attempt_at, status, created_at, sent_at
FROM _notification_outbox_old;

DROP TABLE _notification_outbox_old;

CREATE INDEX idx_outbox_pending_due
    ON notification_outbox(status, next_attempt_at)
    WHERE status = 'pending';
