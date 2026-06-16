-- Admin-approved user activation. New users created by the OAuth
-- callback are pending until an admin approves them. The pending
-- state is encoded by is_active = 0; deactivated_at distinguishes
-- a pending user (NULL) from an admin-deactivated user (set).
-- See Engine approval flow in internal/auth and the team page UI
-- in internal/web.
ALTER TABLE users ADD COLUMN deactivated_at DATETIME;
