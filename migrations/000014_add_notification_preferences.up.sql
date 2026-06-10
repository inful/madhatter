CREATE TABLE notification_preferences (
    member_id       TEXT PRIMARY KEY,
    email_enabled   INTEGER NOT NULL DEFAULT 1,
    disabled_at     DATETIME,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (member_id) REFERENCES team_members(id) ON DELETE CASCADE
);

-- No row means "default" (email_enabled = 1). The application reads
-- the row when present, falls back to enabled=1 when absent. This
-- keeps the read path cheap for the 99% case where nobody has ever
-- unsubscribed.
CREATE INDEX idx_notification_preferences_disabled
    ON notification_preferences(email_enabled)
    WHERE email_enabled = 0;
