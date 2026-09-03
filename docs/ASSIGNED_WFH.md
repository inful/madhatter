# Assigned WFH

The Support Rota now enforces a **seat cap** on the office. When more
people are scheduled to be on-site than the office has seats, the system
assigns extra people to **work from home** that day. Any assigned WFH
can be **swapped** with an on-site teammate, the same way HAT swaps work
today.

This document describes what changes for users. The implementation plan
is in [`plans/assigned-wfh-plan.md`](../plans/assigned-wfh-plan.md).

## What's new

- **Assigned WFH.** A new way to be WFH on a day. The system allocates it
  to you when the office would otherwise exceed the seat cap. You see it
  in your WFH list with a yellow **Assigned** badge.
- **Swap mechanic.** If you've been assigned WFH on a day and you need to
  be in the office, ask an on-site teammate to swap with you. They
  accept; the Assigned WFH transfers to them. The cap is still met — one
  out, one in.
- **Exempt flag.** Admins can mark members as exempt from automatic
  assignment. An exempt member is never picked for involuntary WFH. They
  can still volunteer via swap.
- **Co-presence tiebreaker.** The picker prefers members who have been
  on-site with the cohort recently. If you haven't been in the office
  with the same group in a while, the system is more likely to keep you
  on-site so you meet them in person.

## What changes for you

### As a regular team member

- **Your WFH list** now shows four colours:
  - Green **Approved** — your own request that was approved.
  - Yellow **Assigned** — a system-assigned WFH. You can swap, not
    withdraw.
  - Purple-blue **Admin-marked** — an admin recorded this WFH on
    your behalf (e.g., you called in sick and forgot to report).
    Same withdrawal rules as Approved (you can withdraw it
    yourself up until midnight of the day).
  - Grey **Withdrawn** — past or cancelled.
- **You can withdraw** approved WFHs (your own requests) as before.
- **You cannot withdraw** an Assigned WFH. The "Withdraw" button is
  replaced with "Request swap" on assigned rows.
- **You can swap** an Assigned WFH with someone who's on-site that day.
  Pick the teammate from the dropdown; they get a notification and can
  accept or reject. Once accepted, the day is yours on-site and theirs
  on WFH. The quota is preserved either way.
- **Your quota does not count Assigned WFHs.** A 2-days-per-period
  voluntary quota stays 2 days. An Assigned WFH shows up in your list
  but doesn't burn your quota.
- **You may receive an email** when you've been assigned WFH. The email
  links directly to the swap form. Per-member email opt-out is honoured
  (existing `notification_preferences` setting).

### As an admin

- **Seat cap** is an env var (`WFH_SEAT_CAP`). Set it to your
  seat count. If unset, the system is a no-op (existing behaviour).
- **Co-presence metric** is on by default. Three env vars tune it:
  - `WFH_COPRESENCE_ENABLED` — kill switch.
  - `WFH_COPRESENCE_HORIZON_DAYS` — how far back the picker scans
    (default 14 calendar days — about two work weeks).
  - `WFH_COPRESENCE_RETENTION_DAYS` — how long rows are kept (default 30 calendar days).
- **Assignment kill switch** is `WFH_ASSIGNMENT_ENABLED` (default on).
- **Exempt a member** from the team-member edit form. New checkbox:
  "Exempt from assigned WFH". An exempt member is never picked for
  involuntary assignment, but they can still volunteer via swap.
- **Reassign an Assigned WFH** from `/admin/wfh`. The "Reassign" button
  opens a small picker. The cap is preserved.

## How the picker decides

When the on-site headcount would exceed the cap, the picker picks the
**excess** members to assign WFH. The priority is:

1. **Fewest WFHs in the current period** (including recurring). The
   member who has spent the least time WFH this period goes first.
2. **Co-presence tiebreaker.** Among members with the same period
   count, the member who has been on-site with the would-be on-site
   cohort most recently goes first. Members who haven't been on-site
   with the cohort in a while are kept on-site.
3. **Alphabetical.** The deterministic tiebreaker.

## What changes for the rota

- **Calendar feeds** continue to work. Assigned WFHs appear in the
  affected member's calendar with a description footer:
  > "This is a system-assigned WFH. Request a swap if you need to come in."
  Calendar clients don't render colour, so the language carries a
  `(assigned)` suffix on the WFH event description for system-assigned
  days and `(swap)` for swap-target days, so subscribers can tell them
  apart from self-requested ones. Admin-marked WFHs keep their existing
  `(marked by admin)` note. Operators can override the text and HTML via
  the standard `CALENDAR_TEMPLATE_*` env vars.
- **Dashboard "Today" panel** continues to show who is on-site, on
  leave, and WFH. Assigned WFHs appear in the WFH list with a small
  chip.
- **API** (`/api/v1/wfh`) exposes the new `origin` field on every WFH
  row. Values: `ad_hoc`, `recurring`, `assigned`, `swap`.

## Edge cases

- **No eligible on-site teammate to swap with.** The Assigned WFH
  stands. You can keep the day, ask an admin to reassign, or wait for
  the team size to change.
- **A swap is pending past its date.** The system auto-cancels it.
  You're on-site that day (the Assigned WFH effectively became a no-op).
- **You start as "exempt" by default and an admin toggles it off.** Next
  time the office is over capacity, the picker considers you. If you've
  taken 0 voluntary WFHs this period, you're likely picked first.
- **WFH multi-day swaps.** Each date is a separate swap. If you've been
  Assigned WFH three days in a row, you get three swap requests (or
  three swap actions).

## Configuration

| Setting | Default | What it does |
|---|---|---|
| `WFH_SEAT_CAP` | _unset_ | Seat cap. Unset = no enforcement. |
| `WFH_ASSIGNMENT_ENABLED` | `true` | Master switch for the assignment step. |
| `WFH_COPRESENCE_ENABLED` | `true` | Master switch for the co-presence tiebreaker. |
| `WFH_COPRESENCE_HORIZON_DAYS` | `14` | Calendar days the picker scans back. |
| `WFH_COPRESENCE_RETENTION_DAYS` | `30` | Calendar days co-presence history is kept. |

## Things to expect after rollout

- **The first week or two will feel "cold".** The co-presence
  tiebreaker degrades gracefully — until 14 days of history are
  accumulated, the picker falls back to "fewest WFHs in the period,
  alphabetical".
- **Members who take very few voluntary WFHs will be picked first when
  the office is over capacity.** This is by design: the burden rotates
  to whoever has spent the least time out.
- **Exempt members never get assigned WFH.** They can still be on the
  receiving end of a swap (voluntary).
- **The picker is deterministic.** Same inputs → same picks. The
  alphabetical tiebreaker guarantees a stable order across re-runs.

### Swap notification + duplicate-guard semantics

- **Swap events fire emails.** When a WFH-swap is created, accepted,
  rejected, or cancelled, the requester / target receives an email via
  the same swap-notification pipeline HAT swaps use. The email
  templates are shared between HAT and WFH swaps; the default body
  text reads generically ("swap days", not "HAT days") so a single
  template set covers both. Per-member email opt-out via
  `notification_preferences` is honoured.
- **A second swap on the same assigned row is refused with 409.** If
  you've already requested a swap and want to redirect, cancel the
  first one from your WFH list before sending another.
- **No silent auto-canvas-create.** The existing cap math + 409 guard
  on the swap row itself is the only safety against accidental
  stacking — we don't add a transaction-level lock.

## Status

All planned phases (`plans/assigned-wfh-plan.md` steps 1–22) have
landed. The three original open questions were resolved as follows:
- Calendar summary: kept in the description only (the `(assigned)` /
  `(swap)` / `(marked by admin)` suffixes carry the same information
  in the alt-desc; the SUMMARY stays short).
- "You've been assigned WFH" dashboard banner: not implemented. The
  yellow **Assigned** chip in the WFH list and the swap button on the
  row surface the same signal without a dedicated banner.
- Swap inbox: a separate page (`/wfh/swap/inbox`) reachable from the
  Quick Actions menu.

For questions, open an issue or see the implementation plan for
phase-by-phase context.
