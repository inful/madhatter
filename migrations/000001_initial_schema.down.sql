-- Rollback initial schema
-- This migration drops all tables in reverse dependency order

-- Drop indexes first
DROP INDEX IF EXISTS idx_api_tokens_active;
DROP INDEX IF EXISTS idx_api_tokens_token_hash;
DROP INDEX IF EXISTS idx_api_tokens_user;
DROP INDEX IF EXISTS idx_oauth_tokens_user_provider;
DROP INDEX IF EXISTS idx_sessions_expires;
DROP INDEX IF EXISTS idx_sessions_token;
DROP INDEX IF EXISTS idx_users_provider;
DROP INDEX IF EXISTS idx_users_email;
DROP INDEX IF EXISTS idx_calendar_subscriptions_token;
DROP INDEX IF EXISTS idx_rota_assignments_member;
DROP INDEX IF EXISTS idx_rota_assignments_date;
DROP INDEX IF EXISTS idx_leave_records_date;

-- Drop tables in reverse dependency order
DROP TABLE IF EXISTS api_tokens;
DROP TABLE IF EXISTS oauth_tokens;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS calendar_subscriptions;
DROP TABLE IF EXISTS rota_assignments;
DROP TABLE IF EXISTS leave_records;
DROP TABLE IF EXISTS team_members;
