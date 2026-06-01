ALTER TABLE team_members ADD COLUMN recurring_wfh_monday INTEGER NOT NULL DEFAULT 0;
ALTER TABLE team_members ADD COLUMN recurring_wfh_tuesday INTEGER NOT NULL DEFAULT 0;
ALTER TABLE team_members ADD COLUMN recurring_wfh_wednesday INTEGER NOT NULL DEFAULT 0;
ALTER TABLE team_members ADD COLUMN recurring_wfh_thursday INTEGER NOT NULL DEFAULT 0;
ALTER TABLE team_members ADD COLUMN recurring_wfh_friday INTEGER NOT NULL DEFAULT 0;

UPDATE team_members
SET recurring_wfh_monday = CASE WHEN is_permanent_wfh = 1 THEN 1 ELSE recurring_wfh_monday END,
    recurring_wfh_tuesday = CASE WHEN is_permanent_wfh = 1 THEN 1 ELSE recurring_wfh_tuesday END,
    recurring_wfh_wednesday = CASE WHEN is_permanent_wfh = 1 THEN 1 ELSE recurring_wfh_wednesday END,
    recurring_wfh_thursday = CASE WHEN is_permanent_wfh = 1 THEN 1 ELSE recurring_wfh_thursday END,
    recurring_wfh_friday = CASE WHEN is_permanent_wfh = 1 THEN 1 ELSE recurring_wfh_friday END;