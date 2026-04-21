-- Add last_used_at column to calendar_subscriptions to track when a subscription
-- was last accessed, enabling cleanup of stale subscriptions.
ALTER TABLE calendar_subscriptions ADD COLUMN last_used_at DATETIME;
