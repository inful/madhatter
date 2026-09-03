-- Swap table for the seat-cap picker (Phase 3 of
-- plans/assigned-wfh-plan.md). When the picker assigns WFH to a
-- member, that member can request a swap to a willing on-site
-- teammate. The swap record tracks the requester (the assigned
-- member), the target (the willing on-site teammate), the date
-- the WFH is on, and the swap's status.
--
-- The cap stays met across the swap: the original assigned WFH
-- is withdrawn (status=withdrawn, withdrawn_by='swap:<id>')
-- and a new WFH row is inserted for the target with
-- origin='swap'. Single-transaction update so a failed target
-- insert leaves the assigned WFH intact.
--
-- The unique-ish invariant: there can only be ONE pending swap
-- per assigned wfh_request at a time. Multiple pending swaps on
-- the same row would let the original assignee double-dip the
-- same WFH. The web form rejects this on submit (409 Conflict)
-- and the storage layer enforces it via the
-- idx_wfh_assignment_swaps_target index plus the application's
-- pre-insert check.
CREATE TABLE IF NOT EXISTS wfh_assignment_swaps (
    id TEXT PRIMARY KEY,
    requester_wfh_request_id TEXT NOT NULL,
    target_member_id TEXT NOT NULL,
    swap_date DATE NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'accepted', 'rejected', 'cancelled')),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    resolved_at DATETIME,
    FOREIGN KEY (requester_wfh_request_id) REFERENCES wfh_requests(id) ON DELETE CASCADE,
    FOREIGN KEY (target_member_id) REFERENCES team_members(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_wfh_assignment_swaps_target ON wfh_assignment_swaps(target_member_id, status);
CREATE INDEX IF NOT EXISTS idx_wfh_assignment_swaps_date ON wfh_assignment_swaps(swap_date);
CREATE INDEX IF NOT EXISTS idx_wfh_assignment_swaps_requester ON wfh_assignment_swaps(requester_wfh_request_id);
