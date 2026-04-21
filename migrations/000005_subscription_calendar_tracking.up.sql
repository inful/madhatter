-- Track per-calendar last-used timestamps on calendar_subscriptions so the
-- team page can show whether each member's subscription is actively fetching
-- the rota calendar and/or the meetings calendar.
ALTER TABLE calendar_subscriptions ADD COLUMN last_used_rota_at DATETIME;
ALTER TABLE calendar_subscriptions ADD COLUMN last_used_meetings_at DATETIME;
