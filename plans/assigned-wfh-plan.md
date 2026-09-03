# Assigned WFH — Implementation Plan

## Goal

Enforce a hard seat cap on the office by inserting system-allocated **Assigned WFH** rows when on-site headcount would exceed the cap. Members swap out of an Assigned WFH the same way they swap HAT days (user-to-user, requester-driven). The picker's tiebreaker uses a **co-presence metric** so members who haven't been on-site with the cohort recently are kept on-site, and the picker rotates the burden across the team.

## Non-Goals (v1)

- Pairwise co-presence scoring (per-member-vector).
- Pods / static rotations.
- Live admin UI for the cap (env var only).
- Multi-day swaps.
- On-site check-in/out signals (the metric is roster-based, not physical-presence-based).

## Locked Decisions

| # | Decision |
|---|---|
| 1 | Swap requester-driven (same as HAT swaps) |
| 2 | Swap target pool = on-site members that day |
| 3 | Multiple pending swaps on a row: block with 409 |
| 4 | Swap pending past its date: auto-cancel |
| 5 | Notifications honour per-member `notification_preferences` |
| 6 | Admin override: "Reassign" button on `/admin/wfh` |
| 7 | `/api/v1/wfh` response exposes `origin` |
| 8 | First-day cold start: deterministic alphabetical tiebreaker |
| 9 | Exempt flag default: members can be assigned by default |
| 10 | Exempt and permanent-WFH: separate checkboxes |
| 11 | Swap table: `wfh_assignment_swaps` |
| 12 | Multiple pending swaps: block |
| 13 | Exempt members still subject to the cap (their voluntary WFHs count) |
| 14 | Co-presence ships in v1 with both data collection and the picker tiebreaker |
| 15 | Horizon = 14 calendar days (~2 work weeks); score uses calendar days not working days |
| 16 | Retention = 30 calendar days |
| 17 | Co-presence kill switch `WFH_COPRESENCE_ENABLED` defaults to `true` |
| 18 | Seat-cap env var: `WFH_SEAT_CAP` (renamed from `WFH_MAX_ONSITE_ABSOLUTE` for clarity, since `WFH_MIN_ONSITE_ABSOLUTE` already exists as a floor) |
| 19 | Voluntary quota counter excludes `origin='assigned'` rows (matches the user-facing promise that "your quota does not count Assigned WFHs") |
| 20 | Picker iteration in `SettlePendingRequests` runs over the full settlement window, not just dates with pending requests |
| 21 | Picker is prospective only — `AssignWFHForDate(past_date)` is a no-op |
| 22 | Co-presence history-clamp: real scores above `horizon_days` collapse to `horizon_days + 1`, same as the no-history sentinel |
| 23 | Co-presence backfill is eventually consistent (`INSERT OR IGNORE`); late-arriving rows don't rewrite prior writes |
| 24 | Cap short-falls log a structured warning; do not retry, do not notify per-member |
| 25 | `wfh_requests.withdrawn_by` FK enforces raw `actorUserID`, not a prefixed string like `"reassign:<actor>"`; the "reassign" nature is surfaced via the notifier's `ActorName` suffix |
| 26 | `WFHSwapStatus` must be a typed string (`type WFHSwapStatus string` + `const ... WFHSwapStatus = "..."`), not bare string constants — bare consts are exported but the named type isn't, breaking handler signatures that reference the type |
| 27 | `GetPendingWFHSwapForRequesterRow` is a "exists?" query — returns `(nil, nil)` on no rows; `GetWFHAssignmentSwapByID` keeps the `ErrWFHNotFound`-on-no-rows convention |
| 28 | Cyclop extraction pattern: functions exceeding the 10-branch budget get broken into named helpers (`…Validate`, `…CheckEligibility`, `…Notify`, `…ResultID`) so the orchestrator stays a short composer |
| 29 | `GetLatestCoPresenceWithCohort` is implemented as two `:many` queries with `ORDER BY … DESC LIMIT 1`, not as one `:one` with `SELECT MAX(...)`; sqlc v1.28 returns `interface{}` for `MAX(...)` over a complex `WHERE` |
| 30 | `pickerCohortIDs` includes candidates themselves, not just the post-pick leftover set (which would be circular) |
| 31 | `swap_date` is stored as datetime (not DATE) — test fixtures must pass `time.Time`, not string, for swap_date lookups against the ncruces SQLite driver |
| 32 | The admin reassign audit trail lives in `notifier.WFHStateChanged(ActorName="… (reassign)")`, not in `withdrawn_by` (which is FK-bound to `users(id)`) |
| 19 | Voluntary quota counter excludes `origin='assigned'` rows (matches the user-facing promise that "your quota does not count Assigned WFHs") |
| 20 | Picker iteration in `SettlePendingRequests` runs over the full settlement window, not just dates with pending requests |
| 21 | Picker is prospective only — `AssignWFHForDate(past_date)` is a no-op |
| 22 | Co-presence history-clamp: real scores above `horizon_days` collapse to `horizon_days + 1`, same as the no-history sentinel |
| 23 | Co-presence backfill is eventually consistent (UNIQUE-deduped `INSERT OR IGNORE`); late-arriving rows don't rewrite prior writes |
| 24 | Cap short-falls (excess > len(candidates)) log a structured warning; do not retry, do not notify per-member |

---

## 1. Schema (four migrations)

> **Migration numbering note.** Existing migrations occupy slots 000001–000023. The new migrations land at the next free slots: 000024, 000025, 000026, 000027. Every cross-reference below (status tracker, test strategy, implementation order) uses these slot numbers. Migration `000025` (`wfh_assignment_swaps`) is intentionally landed **after** the picker is wired — it depends on `wfh_requests.origin` but not on any picker-internal type — so the implementation order has 000024 → 000026 → 000027 → 000025 (out of numeric order). Each migration's `.up.sql` is paired with a `.down.sql` that uses the table-rename-and-rebuild recipe (the codebase convention, see `000023_add_wfh_denial_reason.down.sql` for the pattern).

### `000024_add_wfh_request_origin.up.sql`
```sql
ALTER TABLE wfh_requests ADD COLUMN origin TEXT NOT NULL DEFAULT 'ad_hoc'
    CHECK (origin IN ('ad_hoc', 'recurring', 'assigned', 'swap'));
UPDATE wfh_requests SET origin = 'recurring' WHERE is_recurring = 1;
-- Origin is read on every picker / quota / API call. Covering
-- composite index keeps quota queries O(period) instead of
-- full-table scans on history.
CREATE INDEX IF NOT EXISTS idx_wfh_requests_origin_date
    ON wfh_requests(origin, date);
```

### `000025_create_wfh_assignment_swaps.up.sql`
```sql
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
```

### `000026_add_team_member_assignment_exempt.up.sql`
```sql
ALTER TABLE team_members ADD COLUMN is_exempt_from_assignment INTEGER NOT NULL DEFAULT 0;
```

`DEFAULT 0` means "not exempt" = "can be assigned".

### `000027_create_wfh_co_presence.up.sql`
```sql
CREATE TABLE IF NOT EXISTS wfh_co_presence (
    co_presence_id TEXT PRIMARY KEY,
    working_date DATE NOT NULL,
    member_id_a TEXT NOT NULL,
    member_id_b TEXT NOT NULL,
    recorded_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (member_id_a) REFERENCES team_members(id) ON DELETE CASCADE,
    FOREIGN KEY (member_id_b) REFERENCES team_members(id) ON DELETE CASCADE,
    UNIQUE(working_date, member_id_a, member_id_b),
    CHECK (member_id_a < member_id_b)
);
CREATE INDEX IF NOT EXISTS idx_wfh_co_presence_date     ON wfh_co_presence(working_date);
CREATE INDEX IF NOT EXISTS idx_wfh_co_presence_member_a ON wfh_co_presence(member_id_a, working_date);
CREATE INDEX IF NOT EXISTS idx_wfh_co_presence_member_b ON wfh_co_presence(member_id_b, working_date);
```

Canonical ordering (`a < b`) halves the row count and removes the symmetric-pair problem.

---

## 2. Config

> **Naming clarification.** `WFH_MIN_ONSITE_ABSOLUTE` (existing) is a **floor**: settlement denies a WFH request if approving it would drop on-site below this number. The new `WFH_SEAT_CAP` is a **ceiling**: the picker assigns WFH when on-site would otherwise exceed this number. Both are "absolute" counts but they enforce opposite directions. The renaming `WFH_MAX_ONSITE_ABSOLUTE → WFH_SEAT_CAP` makes the intent obvious and avoids the foot-gun of swapping floor and cap in env config.

| Var | Default | Meaning |
|---|---|---|
| `WFH_SEAT_CAP` | _unset_ | Maximum on-site headcount. If unset, assignment step is a no-op. |
| `WFH_ASSIGNMENT_ENABLED` | `true` | Kill switch for the assignment step. |
| `WFH_COPRESENCE_ENABLED` | `true` | Kill switch for the co-presence tiebreaker. |
| `WFH_COPRESENCE_HORIZON_DAYS` | `14` | Calendar days the picker scans. 14 ≈ 10 working days. |
| `WFH_COPRESENCE_RETENTION_DAYS` | `30` | Calendar days rows are kept. Must be ≥ horizon. |

Env loader validates `WFH_COPRESENCE_RETENTION_DAYS >= WFH_COPRESENCE_HORIZON_DAYS` and errors out on boot if not.

**Config struct field:** `Config.SeatCap int` — when 0, the assignment step is a no-op. The picker consults `s.cfg.SeatCap` directly; no shadow field.

---

## 3. Picker algorithm

```
INPUT date
-- Guard: picker is prospective only. Past dates are immutable.
IF date < today: RETURN

totalActive       = count(team_members WHERE is_active = 1)
onLeaveSet        = {m.id | leave_records overlap date AND status IN (pending, assigned)}
permanentWFHSet   = {m.id | team_members.is_permanent_wfh = 1}
-- All approved WFH rows for this date, regardless of origin.
-- Includes previously-assigned rows from earlier picker runs so the
-- on-site count is correctly reduced (a re-run cannot keep assigning
-- WFH to members who already hold an assigned row).
approvedWFHSet    = {m.id | wfh_requests WHERE date = ? AND status = 'approved'}
onSite            = totalActive - |onLeaveSet ∪ permanentWFHSet ∪ approvedWFHSet|
cap               = WFH_SEAT_CAP  (or skip if unset)
IF onSite <= cap: RETURN
excess = onSite - cap

candidates = team_members
  WHERE is_active = 1
    AND is_permanent_wfh = 0
    AND is_exempt_from_assignment = 0
    AND id NOT IN onLeaveSet
    AND id NOT IN approvedWFHSet (any origin)
    AND NOT EXISTS (wfh_requests WHERE member_id = id AND date = ?)

IF WFH_COPRESENCE_ENABLED:
    onSiteCohort = members getting on-site today (the leftover on-site set after picking)
ELSE:
    onSiteCohort = empty  -- tiebreaker degenerate to score=0

-- For weekend / holiday dates, the on-site set is empty after
-- permanent-WFH and on-leave are subtracted. Every candidate scores
-- horizon_days + 1 (no co-presence history with an empty cohort)
-- and the picker degenerates to (periodWFHCount, alphabetical).
-- Documented in section 16 ("First-week cold start") as the same
-- fallback.

for each c in candidates:
    -- periodWFHCount excludes origin='assigned' rows so an Assigned
    -- WFH doesn't burn the member's voluntary quota. The doc promises
    -- "Your quota does not count Assigned WFHs." The query needs to
    -- filter origin. (Existing GetWFHRequestsUsedInPeriod does NOT
    -- filter; we need a sibling GetWFHRequestsVoluntaryInPeriod or
    -- a parameter on the existing query.)
    c.periodWFHCount = count(wfh_requests IN current_period
                             WHERE status IN ('approved','pending')
                               AND origin != 'assigned')
    c.score = IF WFH_COPRESENCE_ENABLED:
        Service.ComputeCoPresenceScore(c, date, onSiteCohort)
    ELSE: 0

ORDER BY periodWFHCount ASC, score ASC, member_name ASC
ordered_candidates = candidates (after the ORDER BY)

IF excess > len(ordered_candidates):
    -- Cap cannot be met with the current candidate pool (e.g., most
    -- members are exempt or on leave). Log a warning with the gap
    -- and the cap short-fall; the picker picks what it can. The
    -- on-call admin sees the warning in the scheduler's structured
    -- log output and can intervene manually.
    slog.Warn("WFH picker: cap short-fall",
        "date", date,
        "excess", excess,
        "candidates", len(ordered_candidates),
        "short_by", excess - len(ordered_candidates))
    picks = ordered_candidates   -- all of them, capped at len(candidates)
ELSE:
    picks = ordered_candidates[:excess]

FOR p IN picks:
    INSERT wfh_requests (status='approved', origin='assigned', date=date, member_id=p.id)
    fire WFHStateChanged event with origin='assigned'
    -- On INSERT failure (transient DB error, UNIQUE collision from a
    -- concurrent picker): slog.Error with the member id and date,
    -- continue to the next pick. The cap short-fall is then larger
    -- than the warning suggested; an admin sees both signals in the
    -- scheduler log.
```

`exempt_from_assignment = 1` members are excluded from `candidates` but still counted in `periodWFHCount` (and still subject to the cap math, since their voluntary WFHs reduce the on-site pool). Their co-presence rows are still written (they participate in the historical record).

**Idempotency.** Two layers:
1. The candidate filter `NOT EXISTS (wfh_requests WHERE member_id = id AND date = ?)` skips anyone who already has any WFH row for the date — pending, approved, recurring, assigned, or swap. So a re-run for the same date sees zero candidates that already have rows.
2. The `UNIQUE(member_id, date)` constraint backstops (1) — if two pickers race and both pick the same member, the loser sees a duplicate-key error.

**Re-run correctness.** The `approvedWFHSet` in the on-site math now includes all origins, so a previously-assigned row correctly subtracts that member from `onSite`. On a re-run for the same date, `approvedWFHSet` already contains the assigned member, so `excess` reflects the post-assignment count. If the cap is already met, `IF onSite <= cap: RETURN` exits. If the cap is still exceeded (more members need to be assigned), only the additional members are picked — the originally-assigned ones are filtered out by `NOT EXISTS`.

**Origin in `periodWFHCount`.** Voluntary quota is the user-facing promise that "2 days per period" means 2 days of *voluntary* WFH. Counting `origin='assigned'` rows against the quota would silently contradict the user-facing doc. The picker's `periodWFHCount` therefore filters `origin != 'assigned'`. This requires a sibling SQLC query (`GetWFHRequestsVoluntaryInPeriod`) or a parameter on the existing one — see step 1 of section 15 ("Implementation order").

---

## 4. Co-presence score

`Service.ComputeCoPresenceScore(c, date, onSiteCohort)`:

```sql
SELECT MAX(cp.working_date)
FROM wfh_co_presence cp
WHERE cp.working_date >= date - :horizon_days
  AND cp.working_date <  date
  AND (
        (cp.member_id_a = c.id AND cp.member_id_b IN :on_site_cohort)
     OR (cp.member_id_b = c.id AND cp.member_id_a IN :on_site_cohort)
  )
```

If NULL → **`min(date - oldest_known_date, horizon_days) + 1`** (see "Sentinel and history-clamp" below). If non-NULL → `date - max_date` **calendar days** (intentional, see "Calendar days vs working days" below).

`date < today` filter ensures the metric only consults committed past attendance, not today's prediction.

**Calendar days vs working days.** The score is computed in **calendar days**, not working days. The user-facing doc uses the "≈ 10 working days" intuition for the 14-day horizon (14 calendar ≈ 10 working when the window spans two work weeks), but the score itself is calendar days. Working-day math requires walking a calendar and skipping weekends/holidays; the cost is not justified for a tiebreaker. The user-facing doc must say "calendar days" instead of "working days" — patched in section 14 of this plan.

**Sentinel and history-clamp.** A raw `999` sentinel has two problems:

1. A member whose last co-presence was 1500 days ago (well outside the horizon) gets `score = 1500`, which is **better** (lower) than the `999` sentinel assigned to someone who never co-presented with the cohort. The `ORDER BY score ASC` policy ("lower score = picked first") then ranks the ancient-history member *before* the no-history member — the opposite of the intent.
2. The horizon is a hard cutoff (`working_date >= date - horizon_days`), so any value above `horizon_days` is "effectively no history" for ordering purposes.

Fix: clamp the score to `min(working_days_since_last, horizon_days) + 1`. A real score of 1500 collapses to `horizon + 1 = 15`, same as the sentinel. The `+ 1` keeps "never co-present" slightly worse than "co-present exactly horizon_days ago" so the tiebreaker prefers someone who was with the cohort yesterday over someone who has no record. The sentinel is no longer a magic number 999; it's the natural horizon-edge value.

In pseudocode:

```
raw = working_days_since_last_co_presence_with_cohort
IF raw IS NULL: raw = horizon_days + 1   -- "no history"
RETURN min(raw, horizon_days + 1)
```

**Empty cohort.** If `onSiteCohort = ∅` (weekend / holiday / everyone on leave), every candidate has `score = horizon_days + 1` (no co-presence history with an empty cohort). The picker degenerates to `(periodWFHCount, alphabetical)`, which is the same fallback the first-week cold start uses. Documented in section 16.

---

## 5. Settlement pipeline

`SettlePendingRequests` (existing) gains four new steps. The picker loop is **independent of `byDate`** (which only contains dates with pending requests) — see "Picker iteration" below:

```
1. Materialize recurring WFHs                       [existing]
2. Approve/deny pending requests                    [existing]
3. AssignWFHForDate(date) for each date in window   [NEW]
4. BackfillCoPresence(last 7 working days)          [NEW]
5. PruneCoPresenceOlderThan(retention_days)         [NEW]
6. Purge past periods                               [existing]
7. Auto-cancel swaps with swap_date < today         [NEW]
```

**Picker iteration.** The settlement window is `[today, today + SettlementDays]`. Step 3 iterates **every working day in that window**, not just the dates with pending requests — a date can be over-cap even with zero pending WFH (everyone is on-site and the cap is exceeded). The existing `settleDate` (step 2) keeps its `byDate` iteration; the new picker step has its own loop over the settlement window.

```go
for d := today; !d.After(cutoff); d = d.AddDate(0, 0, 1) {
    if !isWorkingDay(d) { continue }
    if err := s.AssignWFHForDate(ctx, d.Format("2006-01-02")); err != nil {
        slog.Error("WFH assignment failed", "date", d, "error", err)
    }
}
```

`isWorkingDay(d)` skips weekends and holidays (the existing `HolidayLookup` interface and weekday check). On a weekend or holiday the picker is a no-op (the cap is irrelevant; nobody is scheduled to be on-site).

---

## 6. On-demand trigger

The `presenceBuilder` grows two responsibilities with this change: (a) write committed past attendance as co-presence rows for the picker tiebreaker, and (b) ensure assigned WFHs are filled in when the user's page load shows a future date that already exceeds the cap. Both responsibilities are *settlement hooks*, not snapshot work — splitting the orchestrator into two methods makes the contract explicit.

The `Build` method on `presenceBuilder` is renamed to **`SnapshotFor`** and its responsibility narrows: "compute a snapshot for `date` and return it." No settlement hooks run from `SnapshotFor`. Two new methods are added:

```go
// SnapshotFor computes the presence snapshot for dateStr without
// touching the database beyond reads. Idempotent and safe to call
// for past, today, and future dates.
func (b *presenceBuilder) SnapshotFor(ctx context.Context, dateStr string) (*presenceSnapshot, error)

// RefreshFor is the settlement hook. It (a) materializes recurring
// rows for dateStr, (b) calls AssignWFHForDate for dateStr if
// dateStr is today or future (past dates are immutable — see guard
// in section 3), and (c) writes wfh_co_presence pair rows for the
// on-site set if dateStr is strictly in the past (committed
// attendance only; predictions are not yet committed). Idempotent
// in all three sub-steps via UNIQUE constraints on
// wfh_co_presence and wfh_requests.
func (b *presenceBuilder) RefreshFor(ctx context.Context, dateStr string) (*presenceSnapshot, error)
```

RefreshFor composes: `SnapshotFor` plus the three settlement hooks. Existing call sites that only need a snapshot (calendar feed, dashboard) can opt out of the settlement hooks; existing call sites that already expected the side-effects (per-member `materializeRecurring`) continue to get them via `RefreshFor`. The on-demand entry point that "ensure the user's page load sees fresh assignments" calls `RefreshFor`.

The split keeps cyclomatic complexity on `SnapshotFor` low (it's a read-only computation) and isolates the settlement hooks behind a method whose name declares the side effects. The plan's pseudocode in section 3 ("Picker algorithm") is the implementation of `AssignWFHForDate`, called from step 3 of `RefreshFor`.

**Co-presence writes are strictly past.** `RefreshFor(date)` only writes `wfh_co_presence` rows when `date < today`. For `date >= today`, no co-presence write — the snapshot is a prediction, not committed attendance. UNIQUE on `(working_date, member_id_a, member_id_b)` dedupes the writes if `RefreshFor` runs twice for the same date.

**AssignWFHForDate is strictly prospective.** Called from `RefreshFor` only when `date >= today`. Past dates are immutable: the picker must not insert assigned WFH rows for dates that have already been lived. The guard is inside `AssignWFHForDate` itself (section 3) so any future caller is safe by default; the on-demand trigger is one of those callers.

---

## 7. Withdraw gate

`WithdrawOwnWFHRequest` returns `ErrWFHAssigned` for `origin IN ('assigned', 'swap')`:

```go
if req.Origin == "assigned" || req.Origin == "swap" {
    return ErrWFHAssigned
}
```

---

## 8. Swap mechanism

### Routes

| Method | Path | Handler |
|---|---|---|
| GET | `/wfh/{id}/swap` | `handleWFHSwapForm` |
| POST | `/wfh/{id}/swap` | `handleWFHSwapCreate` |
| POST | `/wfh/swap/{swapId}/accept` | `handleWFHSwapAccept` |
| POST | `/wfh/swap/{swapId}/reject` | `handleWFHSwapReject` |
| POST | `/wfh/swap/{swapId}/cancel` | `handleWFHSwapCancel` |

### Target eligibility

```sql
SELECT 1 FROM team_members m
WHERE m.id = ? AND m.is_active = 1
  AND m.is_permanent_wfh = 0
  AND NOT EXISTS (
    SELECT 1 FROM leave_records
    WHERE member_id = m.id AND start_date <= ? AND end_date >= ?
      AND status IN ('pending','assigned')
  )
  AND NOT EXISTS (
    SELECT 1 FROM wfh_requests
    WHERE member_id = m.id AND date = ?
  )
```

Exempt members are **included** in the swap target list (the swap is voluntary; the target accepts).

### Accept flow (single transaction)

```sql
UPDATE wfh_assignment_swaps SET status='accepted', resolved_at=NOW WHERE id = ?;
UPDATE wfh_requests SET status='withdrawn', withdrawn_by='swap:<swapId>', withdrawn_at=NOW WHERE id = :requester_id;
INSERT INTO wfh_requests (..., status='approved', origin='swap');
-- Fire WFHStateChanged for both events.
```

### Reject / cancel

```sql
UPDATE wfh_assignment_swaps SET status='rejected'|'cancelled', resolved_at=NOW WHERE id = ?;
-- No wfh_requests mutation.
```

### Multiple pending swaps

`handleWFHSwapCreate` rejects with `409 Conflict` if any swap for `requester_wfh_request_id` already has `status='pending'`.

### Auto-cancel on date-passed

A new step in `SettlePendingRequests` updates `wfh_assignment_swaps` with `status='cancelled'` where `swap_date < today AND status='pending'`.

---

## 9. Admin override

`/admin/wfh` gains a "Reassign" button on each assigned row. Form picks a replacement member. In a single transaction:

```sql
UPDATE wfh_requests SET status='withdrawn', withdrawn_by=<admin>, withdrawn_at=NOW WHERE id = :requester_id;
INSERT INTO wfh_requests (..., status='approved', origin='assigned') FOR replacement_id;
-- Fire notifications for both.
```

The cap is preserved (one out, one in). The replacement member is subject to the same picker eligibility (exempt, leave, etc.) but the admin can override.

---

## 10. UI updates

| Template | Change |
|---|---|
| `wfh_list.html` | Assigned rows show a warning "Assigned" badge. Withdraw button → "Request swap" button. |
| `wfh_manage.html` (admin) | Same. Admin rows also get a "Reassign" button for `origin='assigned'`. |
| `wfh_swap.html` (NEW) | Target-picker dropdown form. |
| `wfh_swap_inbox.html` (NEW) | List of pending swaps where the current user is the target. |
| `team_member_edit.html` (admin) | New checkbox "Exempt from assigned WFH". |
| Calendar ICS template | Branch on origin: adds "This is a system-assigned WFH. Request a swap if you need to come in." for `origin='assigned'`. |
| Dashboard "Today" panel | Assigned WFHs appear with a small "Assigned" chip. |

---

## 11. Notification

Extend `notify.WFHEvent` with `Origin string` and the new swap events (`SwapRequested`, `SwapAccepted`, `SwapRejected`, `SwapCancelled`). Branch in the existing template:

| Origin / Event | Subject | Body |
|---|---|---|
| `ad_hoc`/`recurring` | "Your WFH has been approved" | existing |
| `assigned` | "You've been assigned WFH on [date]" | "Request a swap if you need to come in." |
| `swap` (after accept) | "Swap accepted: [date] is yours on-site" | "Swap requester X has transferred the assigned WFH to you." |
| `SwapRequested` | "Swap request from [requester]" | "[requester] is asking you to take their assigned WFH on [date]." |
| `SwapRejected` | "Swap request declined" | "[target] declined your swap request for [date]." |
| `SwapCancelled` | "Swap request cancelled" | "The swap request for [date] was cancelled (auto-cancel because the date passed)." |

Per-member `notification_preferences.email_enabled` opt-out is honoured by the existing wiring. **Cap short-fall warnings** (section 3) are *not* routed to the per-member notification outbox — they're a structured-log signal the scheduler emits, intended for the on-call admin. A future enhancement could route them via a dedicated admin-channel if needed.

---

## 12. API exposure

`/api/v1/wfh` (and any other WFH endpoints) include `origin` in the response. Type `string` with documented enum.

---

## 13. Test strategy

| Layer | Test |
|---|---|
| Migration 24 | Up + down; backfill correctness (existing rows get origin='ad_hoc' or 'recurring' per `is_recurring`). Index `idx_wfh_requests_origin_date` exists post-up. |
| Migration 25 | Up + down; FKs, check constraints, UNIQUE on (date, target_member_id, status) implicit via existing UNIQUE on requester_wfh_request_id+status='pending'. |
| Migration 26 | Up + down; default = 0. |
| Migration 27 | Up + down; UNIQUE on (working_date, member_id_a, member_id_b), CHECK (a < b), FKs. |
| Picker core | `service_assignment_test.go` — 0/1/N candidates, K picks, alphabetical tiebreak, exempt filter. |
| Picker priority | Members with recurring in-period have higher `periodWFHCount` and are picked last. |
| Picker idempotency | Run twice; second call inserts 0. |
| Picker no-op | `onSite <= cap` → no inserts, no log warnings. |
| Picker cap-met | `onSite == cap` exactly → no inserts. |
| Picker cap-exceeded | `onSite == cap + K` → exactly K members picked. |
| Picker cap-shortfall | `excess > len(candidates)` → all candidates picked, slog.Warn emitted with `short_by`. |
| Picker past-date guard | `AssignWFHForDate(yesterday)` is a no-op (returns nil, no rows inserted). |
| Picker periodWFHCount excludes assigned | Member with 2 voluntary + 1 assigned has `periodWFHCount = 2` for picker purposes; quota counter reports the same. |
| Picker iteration window | `SettlePendingRequests` calls `AssignWFHForDate` for every working day in `[today, today+SettlementDays]`, not just dates with pending requests. A date over-cap with no pending requests still gets the picker. |
| On-demand trigger | `presenceBuilder.RefreshFor` runs `AssignWFHForDate` for `date >= today` and `WriteCoPresence` for `date < today`. `SnapshotFor` does neither. |
| Co-presence writer | Past-date RefreshFor writes `wfh_co_presence` rows; future-date RefreshFor does not. |
| Co-presence writer idempotency | Running RefreshFor twice for the same past date writes 0 new rows. |
| Co-presence backfill | Daily backfill writes rows for any unrecorded past working days (eventually-consistent — see "Backfill semantics" in section 16). |
| Co-presence retention | `PruneCoPresenceOlderThan(30)` deletes rows older than the retention window. |
| Co-presence score | `ComputeCoPresenceScore(c, date, cohort)` returns `date - max_last_co_presence_date` in calendar days. |
| Co-presence score history-clamp | A real score of 1500 days (way beyond horizon) is clamped to `horizon_days + 1`, same as the no-history sentinel. |
| Co-presence score default | Returns `horizon_days + 1` when `c` has no co-presence history with the cohort. |
| Co-presence score horizon | Picker only scans rows within `WFH_COPRESENCE_HORIZON_DAYS` of the date. |
| Picker tiebreaker | Given the same `periodWFHCount`, the candidate with the lowest `score` (most recent co-presence) is picked first. |
| Picker tiebreaker empty cohort | Empty `onSiteCohort` (weekend/holiday/all-on-leave) makes every candidate score `horizon_days + 1`; picker degenerates to `(periodWFHCount, alphabetical)`. |
| Picker tiebreaker kill switch | `WFH_COPRESENCE_ENABLED=false` causes the picker to fall back to `score=0` for all candidates. |
| Withdraw gate | `ErrWFHAssigned` for `origin='assigned'` and `origin='swap'`. |
| Swap eligibility | Excludes permanent WFH, on-leave, on-recurring-WFH, on-voluntary-WFH, on-assigned-WFH. Exempt members are included. |
| Swap multiple pending | Second create returns 409. |
| Swap accept | Withdrawal of requester + new row for target in a single transaction. |
| Swap reject | No `wfh_requests` mutation. |
| Swap auto-cancel | Pending swap with `swap_date < today` is cancelled. |
| Quota | `GetQuotaStatus` for 2 voluntary + 1 assigned = `Used: 2/2 voluntary · 1 assigned`. The quota counter query must filter `origin != 'assigned'`. |
| Notification | Fires on assign, on swap request, on swap accept/reject, on admin reassign. |
| Notification preferences | Opted-out member does not receive assignment email. |
| Admin reassign | Reassign flow preserves the cap. |
| Dashboard | Assigned rows appear with badge. |
| Calendar | ICS event description includes "system-assigned" footer. |
| API | `/api/v1/wfh` response includes `origin` field. |
| Exempt toggle | Toggling exemption off causes the next picker pass to assign the member first. |
| Seat-cap env rename | The codebase has no reference to `WFH_MAX_ONSITE_ABSOLUTE`; the new var is `WFH_SEAT_CAP`. Grep-test as part of the implementation commit. |

---

## 14. Documentation

| File | Change |
|---|---|
| `README.md` | Add `WFH_SEAT_CAP`, `WFH_ASSIGNMENT_ENABLED`, `WFH_COPRESENCE_ENABLED`, `WFH_COPRESENCE_HORIZON_DAYS`, `WFH_COPRESENCE_RETENTION_DAYS` to env-var table. Add "Assigned WFH with swap and co-presence" to features list. |
| `internal/web/templates/help.html` | New section "Assigned WFH, how to swap, and how co-presence works." |
| `CONSOLIDATED_REFERENCE.md` | Routes table includes the four swap endpoints and the admin reassign endpoint. Env-var table includes the five new vars. |
| `docs/NOTIFICATIONS.md` | Document the new `assigned` and `swap` event origins. |
| `docs/ASSIGNED_WFH.md` | User-facing digest. Patch to (a) say "calendar days" not "working days" for the horizon (per section 4), (b) clarify that **admin-marked WFHs** (existing `is_admin_marked=1` feature from v0.26.0) render as a fourth colour (`is-link` purple-blue, same as on the dashboard) — they are *Approved* but flagged so the team can see which days were admin-asserted vs self-requested. The colour scheme becomes: green = Approved (self-requested), yellow = Assigned (system-assigned), purple-blue = Admin-marked (admin override of an unrequested day), grey = Withdrawn. |

---

## 15. Implementation order

> **Why migration 000025 (`wfh_assignment_swaps`) lands last, out of numeric order.** Swaps reference `wfh_requests.origin` via the assigned/swap origin values, but they don't depend on the picker or on `wfh_co_presence`. Building the picker + co-presence + assignment surface first lets us verify those work before layering the swap mechanic on top. The four migrations land in the order 000024 → 000026 → 000027 → 000025 (cross-reference the schema in section 1).

1. Migration 000024 (`wfh_requests.origin` + `idx_wfh_requests_origin_date`) + sqlc generate. Extend `WFHRequest` Go struct with `Origin string`.
2. Migration 000026 (`is_exempt_from_assignment`) + sqlc generate. Extend `TeamMember` Go struct.
3. **Quota counter migration.** Add a new SQLC query `GetWFHRequestsVoluntaryInPeriod` (sibling of `GetWFHRequestsByMemberAndPeriod`) that filters `origin != 'assigned'`. Refactor `GetWFHRequestsUsedInPeriod` callers to use the new query where the caller's intent is "user-facing voluntary quota." (The picker uses this query too — section 3.) This step is a behavior change: existing quota counters go from "all approved/pending" to "all approved/pending excluding assigned." The user-facing doc already promises this behavior; the SQL is being brought into line.
4. Migration 000027 (`wfh_co_presence`) + sqlc generate.
5. New `ErrWFHAssigned` and `WithdrawOwnWFHRequest` gate (for `origin IN ('assigned','swap')`). Step 4 of section 7.
6. `SeatCap` config field + env loader. Validation: `WFH_COPRESENCE_RETENTION_DAYS >= WFH_COPRESENCE_HORIZON_DAYS`.
7. `AssignWFHForDate` in `internal/wfh/service.go` with the picker (initial: no co-presence tiebreaker, `score=0` for all). Includes the past-date guard (section 3) and the cap short-fall warning.
8. Wire `AssignWFHForDate` into `SettlePendingRequests` — independent loop over the settlement window (section 5).
9. Refactor `presenceBuilder` into `SnapshotFor` + `RefreshFor`. Wire `RefreshFor` to call `AssignWFHForDate` + `WriteCoPresence`. Update call sites.
10. `ComputeCoPresenceScore` + picker tiebreaker (with the history-clamp, section 4). `WFH_COPRESENCE_ENABLED=false` degrades to `score=0`.
11. `BackfillCoPresence` (eventually-consistent — see section 16) + `PruneCoPresenceOlderThan` in `SettlePendingRequests`.
12. Migration 000025 (`wfh_assignment_swaps`) + sqlc generate. *(Landed last of the four migrations — see the note at the top of this section.)*
13. Swap routes + form + accept/reject/cancel handlers.
14. Swap auto-cancel pass in `SettlePendingRequests`.
15. Admin reassign route on `/admin/wfh`.
16. Team-member edit form: exempt checkbox.
17. Calendar ICS template: branch on origin.
18. API: expose `origin` field.
19. Notification template: branch on origin (with the four new event kinds from section 11).
20. Docs: README, help.html, CONSOLIDATED_REFERENCE.md, NOTIFICATIONS.md, `docs/ASSIGNED_WFH.md` (with the calendar-days and four-colour patches from section 14).
21. Pre-commit checklist: tests + lint + sqlc generate + grep-test that no `WFH_MAX_ONSITE_ABSOLUTE` references remain.

---

## 16. Risks & open considerations

- **Race condition between scheduler tick and on-demand trigger.** Both insert the same row; the UNIQUE constraint ensures only one wins for the same `(member_id, date)`. **Best-effort**: when the two pickers run concurrently with *diverging* views of state (one stale, one fresh), the deterministic alphabetical tiebreaker guarantees identical picks *iff* both see the same cohort. With a real race, the cohort could differ; the stale picker picks fewer candidates, the fresh one fails on conflict, and the cap is left partially unmet for that date. A future enhancement is a per-date advisory lock (`BEGIN IMMEDIATE` in a wrapper transaction) to serialize picker calls; not in v1. The cap short-fall warning (section 3) is the operator signal.

- **Co-presence writer and partial snapshots.** If a past date is built before all WFH/leave data is committed, the co-presence rows might be incomplete. The daily backfill corrects this the next day.

- **Backfill semantics.** Two options were considered:
  1. **Eventually consistent (chosen).** `BackfillCoPresence(last 7 working days)` re-reads current state for each past day and writes pair rows via `INSERT OR IGNORE` on the UNIQUE constraint. Original (possibly incomplete) writes survive because `INSERT OR IGNORE` skips rows that already exist. Late-arriving leave approvals or WFH withdrawals on day N-2 are reflected in subsequent picker scores but not in the original co-presence rows.
  2. **Delete-and-rewrite.** Each backfill pass deletes prior rows for the date and rewrites. Requires dropping the UNIQUE constraint (or doing `DELETE` + `INSERT` separately). Provides exact-current state at backfill time but loses idempotency guarantees and risks concurrent-writer anomalies.

  Option 1 is chosen because (a) idempotency is a hard requirement for the daily scheduler, (b) the picker only uses the score for tiebreaking, so slightly-stale co-presence rows are acceptable, and (c) delete-and-rewrite adds a write-amplification path that complicates concurrency.

- **First-week cold start.** Until the co-presence table has `WFH_COPRESENCE_HORIZON_DAYS` (default 14) of data, `score` is `horizon_days + 1` (the clamped sentinel, section 4) for everyone. The picker falls back to `periodWFHCount, alphabetical` for the first two weeks. Acceptable; documented.

- **Per-member-vector scoring.** The "any cohort member" metric is a first-order approximation. The candidate might be co-present with the cohort every day but always with the same one person. v2 enhancement.

- **Quota semantics in admin reassign.** The admin "Reassign" path adds an assigned WFH to the replacement member; their voluntary quota is unchanged (assigned WFHs don't count). Acceptable; admins are explicit.

- **Retention ≥ horizon invariant.** A boot-time check fails fast if `WFH_COPRESENCE_RETENTION_DAYS < WFH_COPRESENCE_HORIZON_DAYS`. The env loader validates and errors out.

- **Calendar feed and assigned WFHs.** The template branch covers the description. The summary line might also want a "[Assigned]" tag — confirm with the user-facing digest (open question 1 of section 17 below).

- **Pick failure semantics.** When `INSERT wfh_requests ... origin='assigned'` fails (UNIQUE collision from a concurrent picker, or transient DB error), the picker logs and continues to the next candidate. The cap short-fall is then larger than the pre-loop warning suggested. Both signals appear in the scheduler log; the operator sees the gap. The picker does **not** retry, does **not** re-query state mid-loop, and does **not** notify per-member — those would multiply the cost of a transient DB blip into something that affects users. The structured log is the v1 signal.

---

## 17. Status tracker

Steps 1–16 are complete across v0.28.0 / v0.29.0 / v0.30.0.
Steps 17–22 are Phase 4 (target v0.31.0). Migration numbers
are the post-#1 lift: 000024, 000025, 000026, 000027.

| Step | Status | Released | Notes |
|---|---|---|---|
| 1 — Migration 000024 (`wfh_requests.origin` + index) | ✅ | v0.28.0 | Migration 000024 also covers the backfill `origin='recurring' WHERE is_recurring=1`. |
| 2 — Migration 000026 (`is_exempt_from_assignment`) | ✅ | v0.28.0 | DB method `SetTeamMemberExemptFromAssignment` exposed. |
| 3 — Quota-counter migration (`GetWFHRequestsVoluntaryInPeriod`) | ✅ | v0.28.0 | Sibling SQLC query; existing `GetWFHRequestsByMemberAndPeriod` kept for the on-site floor math. |
| 4 — Migration 000027 (`wfh_co_presence`) | ✅ | v0.28.0 | Three indexes (date / member_a+date / member_b+date). |
| 5 — Withdraw gate (`ErrWFHAssigned`) | ✅ | v0.28.0 | WithdrawWFHRequest + WithdrawOwnWFHRequest both reject `origin IN ('assigned','swap')`. |
| 6 — `SeatCap` config + env loader | ✅ | v0.29.0 | Five new env vars; `Config.Validate()` fails fast at boot if `retention < horizon`. |
| 7 — `AssignWFHForDate` (no co-presence yet) | ✅ | v0.29.0 | Past-date guard, cap short-fall warning, voluntary-only `periodWFHCount`. |
| 8 — Wire into settlement (independent loop) | ✅ | v0.29.0 | `settleAssignmentPass` iterates the full settlement window, not just `byDate`. |
| 9 — Refactor presenceBuilder → SnapshotFor + RefreshFor | ✅ | v0.29.0 | New `CoPresenceWriter` interface keeps the calendar package free of wfh dependencies. |
| 10 — Co-presence tiebreaker (with history-clamp) | ✅ | v0.29.0 | Two :many queries with `LIMIT 1` (sqlc v1.28 can't infer `SELECT MAX(...)` over a complex WHERE cleanly). |
| 11 — Co-presence writer (past-date only) | ✅ | v0.29.0 | `RecordWFHCoPresencePair` + `InsertWFHCoPresencePair` (returns inserted-true). |
| 12 — Backfill (eventually-consistent) + retention | ✅ | v0.29.0 | `BackfillCoPresence` in `SettlePendingRequests`; `PruneWFHCoPresenceOlderThan` exposed. |
| 13 — Migration 000025 (`wfh_assignment_swaps`) | ✅ | v0.30.0 | FKs to wfh_requests + team_members (CASCADE), three indexes, `WFHSwapStatus` typed string for status enum. |
| 14 — Swap routes | ✅ | v0.30.0 | GET/POST `/wfh/{id}/swap`, GET `/wfh/swap/inbox`, POST `/wfh/swap/{swapId}/{accept,reject,cancel}`. |
| 15 — Swap auto-cancel | ✅ | v0.30.0 | `AutoCancelExpiredSwaps` in `SettlePendingRequests` after the picker pass. |
| 16 — Admin reassign | ✅ | v0.30.0 | `Service.AdminReassignWFH` + `DB.WithdrawAssignedWFHRequest` (focused bypass of the Phase-1 withdraw gate). |
| 17 — Exempt checkbox | ⏳ | Phase 4 | Team-member edit form's `is_exempt_from_assignment` checkbox. |
| 18 — Calendar ICS branch | ⏳ | Phase 4 | Branch on `origin` in the calendar template — admin-marked WFHs render with the existing Phase-2 distinct color. |
| 19 — API `origin` | ⏳ | Phase 4 | Expose `origin` field on the `/api/v1/wfh` response. |
| 20 — Notification template (4 new event kinds) | ⏳ | Phase 4 | `SwapRequested`, `SwapAccepted`, `SwapRejected`, `SwapCancelled` — actor / target distinct. |
| 21 — Docs | ⏳ | Phase 4 | README, help.html, CONSOLIDATED_REFERENCE.md, NOTIFICATIONS.md, `docs/ASSIGNED_WFH.md`. |
| 22 — Pre-commit | ⏳ | Phase 4 | Tests + lint + sqlc + grep-test that no `WFH_MAX_ONSITE_ABSOLUTE` references remain. |

## 18. Decisions log

### Locked decisions captured at plan-time (review fix #1, post-review pass)

Decisions 18–24 below the original 17. See the **Locked Decisions**
section at the top of this document for the full list.

### Mid-implementation decisions captured during Phase 1–3 (v0.28.0 → v0.30.0)

These were not in the original plan but emerged during the work and
are now locked. Add them to the Locked Decisions section on the next
plan-edit pass.

25. **`withdrawn_by` FK enforces raw actorUserID, not `"reassign:<actor>"`.**
    The `wfh_requests.withdrawn_by` column has `FOREIGN KEY ... REFERENCES users(id)`.
    Storing `"reassign:admin-1"` broke the FK because the prefixed value
    isn't a real `users.id`. Fix: store the raw `actorUserID` (the admin's
    user.id from the auth context) and surface the "reassign" nature
    via the notifier's `ActorName` suffix (`actorName + " (reassign)"`) and
    in the request flow's audit log. The withdrawn_by value is just
    the user.id — no prefix.

26. **`WFHSwapStatus` must be a typed string, not bare constants.**
    Declaring `const (WFHSwapStatusPending = "pending" ...)` as bare
    strings broke the web package's `database.WFHSwapStatus` type
    reference (the bare constants are exported; the named type is not).
    Fix: declare `type WFHSwapStatus string` and make the constants of
    that type. The DB struct's `Status` field stays as `string` (SQLite
    round-trip); the consumer-side use of `WFHSwapStatus` is in handler
    signatures and switch statements where the type matters.

27. **`GetPendingWFHSwapForRequesterRow` is a "exists?" query, not
    a "fetch by ID" query.** The Phase-3 conflict-guard lookup returns
    `(nil, nil)` on no rows (caller treats nil as "no pending swap,
    fine to insert"). `GetWFHAssignmentSwapByID` keeps the
    `ErrWFHNotFound`-on-no-rows convention because callers expect a
    fetched row when asking by ID.

28. **Cyclop extraction pattern for orchestrator functions.**
    Functions exceeding the 10-branch budget get broken up by
    extracting named helpers: `…Validate` (input + ownership +
    origin guards), `…CheckEligibility` (filter check),
    `…Notify` (notification fan-out), `…ResultID` (final lookup).
    The orchestrator then becomes a short orchestrator that
    composes these helpers. Applied in: `AssignWFHForDate`
    (`pastDateGuard`, `isPickerActiveOnDate`, `pickerDisabled`,
    `logCapShortFall`, `insertPicks`, `scoreCandidates`,
    `coPresenceScore`, `sortScored`),
    `SettlePendingRequests` (`settleAssignmentPass`,
    `runPicker`),
    `AdminReassignWFH` (`adminReassignOriginOK`,
    `adminReassignTargetOK`, `adminReassignNotify`,
    `adminReassignResultID`), and
    `handleWFHSwapCreate` (`swapCreateValidate`,
    `swapCreateCheckEligibility`).

### Drift between plan and shipped code

These are documented as design decisions; the implementation
followed the spirit of the plan but adapted the detail. None
represent scope changes — just engineering reality:

29. **`GetLatestCoPresenceWithCohort` is implemented as TWO
    `:many` queries (`…WithCohortA` / `…WithCohortB`) with
    `ORDER BY … DESC LIMIT 1`, not as one `:one` query with
    `SELECT MAX(working_date)`.** sqlc v1.28 returns `interface{}`
    for `SELECT MAX(...)` over a complex `WHERE`, breaking
    the handler-side typed read. The two-query + take-max-in-Go
    approach has the same semantics and is O(1) over the
    horizon indexes.

30. **`pickerCohortIDs` includes candidates themselves**
    (not just the "leftover on-site set after picking").** The
    plan section 4 says "leftover on-site set after picking" but
    that's circular (cohort depends on pick, pick depends on
    cohort). The implementation uses the simpler "everyone not
    leave / permanent / WFH / exempt / the requester" set,
    which includes candidates too. The picker scores each
    candidate against the cohort; the cohort is read ONCE at
    sort time, not after picking. This is the same semantic
    the plan's empty-cohort branch captures.

31. **`swap_date` is stored as datetime, not DATE.** The ncruces
    SQLite driver doesn't coerce a string parameter against a
    datetime column via `=`. Test fixtures pass `time.Time` for
    swap_date lookups. Production code paths (DB wrappers)
    already pass `time.Time`. Documented for future test
    authors who might otherwise write `WHERE swap_date = '2026-09-02'`
    and hit "no rows in result set".

32. **The admin reassign row preserves the audit trail via
    `withdrawn_by = actorUserID` (decision 25) and surfaces the
    "reassign" nature via `notifier.WFHStateChanged(ActorName="…
    (reassign)")`.** The plan's pseudocode used
    `withdrawn_by='reassign:<actor>'` which violates the FK.
    The semantic is the same; only the storage shape changed.
