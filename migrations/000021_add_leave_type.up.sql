-- Add leave_type so users can tag a leave as conference (or future
-- types) for differentiated UI. Defaults to 'leave' so every existing
-- row reads back as the previous behaviour without a backfill. The
-- CHECK constraint keeps the value space closed at the DB layer; the
-- application validates the same set so a bad value never reaches the
-- driver.
ALTER TABLE leave_records ADD COLUMN leave_type TEXT NOT NULL DEFAULT 'leave' CHECK (leave_type IN ('leave', 'conference'));