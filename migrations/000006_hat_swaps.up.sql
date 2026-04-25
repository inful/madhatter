-- Create hat_swaps table for HAT day swap requests between team members.
CREATE TABLE IF NOT EXISTS hat_swaps (
    id                      TEXT PRIMARY KEY,
    requester_assignment_id TEXT NOT NULL,
    target_assignment_id    TEXT NOT NULL,
    requester_member_id     TEXT NOT NULL,
    target_member_id        TEXT NOT NULL,
    status                  TEXT NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending', 'accepted', 'rejected', 'cancelled')),
    created_at              DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at              DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (requester_assignment_id) REFERENCES rota_assignments(id) ON DELETE CASCADE,
    FOREIGN KEY (target_assignment_id)    REFERENCES rota_assignments(id) ON DELETE CASCADE,
    FOREIGN KEY (requester_member_id)     REFERENCES team_members(id) ON DELETE CASCADE,
    FOREIGN KEY (target_member_id)        REFERENCES team_members(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_hat_swaps_requester ON hat_swaps(requester_member_id, status);
CREATE INDEX IF NOT EXISTS idx_hat_swaps_target    ON hat_swaps(target_member_id, status);
CREATE INDEX IF NOT EXISTS idx_hat_swaps_requester_assignment ON hat_swaps(requester_assignment_id, status);
CREATE INDEX IF NOT EXISTS idx_hat_swaps_target_assignment    ON hat_swaps(target_assignment_id, status);

CREATE TRIGGER IF NOT EXISTS trg_hat_swaps_pending_insert
BEFORE INSERT ON hat_swaps
WHEN NEW.status = 'pending'
BEGIN
    SELECT RAISE(FAIL, 'pending swap already exists for one of the assignments')
    WHERE EXISTS (
        SELECT 1
        FROM hat_swaps
        WHERE status = 'pending'
          AND (
              requester_assignment_id IN (NEW.requester_assignment_id, NEW.target_assignment_id)
              OR target_assignment_id IN (NEW.requester_assignment_id, NEW.target_assignment_id)
          )
    );
END;

CREATE TRIGGER IF NOT EXISTS trg_hat_swaps_pending_update
BEFORE UPDATE OF requester_assignment_id, target_assignment_id, status ON hat_swaps
WHEN NEW.status = 'pending'
BEGIN
    SELECT RAISE(FAIL, 'pending swap already exists for one of the assignments')
    WHERE EXISTS (
        SELECT 1
        FROM hat_swaps
        WHERE id <> NEW.id
          AND status = 'pending'
          AND (
              requester_assignment_id IN (NEW.requester_assignment_id, NEW.target_assignment_id)
              OR target_assignment_id IN (NEW.requester_assignment_id, NEW.target_assignment_id)
          )
    );
END;
