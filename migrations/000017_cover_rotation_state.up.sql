-- Stores the cover rotation's last computed state. The cover for any
-- date D is computed as:
--   coverIndex(D) = (state.last_index + working_days(state.last_date, D)) % team_size
-- where working_days excludes weekends and holidays. The engine only
-- ever queries for dates >= state.last_date, so the state is always
-- advanced forward in time. The first call after a fresh database
-- initializes the state at (currentDate, 0), so the initial cover is
-- always members[0] and the rotation advances from there.
CREATE TABLE IF NOT EXISTS cover_rotation_state (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    last_date DATE NOT NULL,
    last_index INTEGER NOT NULL
);
