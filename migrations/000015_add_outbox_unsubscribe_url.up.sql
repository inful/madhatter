-- Adds the per-row unsubscribe URL. The producer fills this in
-- at enqueue time (token is per-recipient), so the worker can
-- stamp the List-Unsubscribe header on every send without having
-- to recompute the URL.
ALTER TABLE notification_outbox ADD COLUMN unsubscribe_url TEXT;
