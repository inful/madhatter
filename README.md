# Support Rota System

A comprehensive support duty management system with automatic scheduling, leave management, calendar subscriptions, and OAuth2 authentication.

## Features

### Core Functionality
- **Round-robin scheduling**: Fair-distribution assignment; weekends and holidays skipped.
- **Leave management**: Unified `LeaveTypeLeave` / `LeaveTypeConference` tagging. Same-day leave via `/leave/report-sick` (any authenticated user; date pinned to today; duplicates refused). Conference leave is web-only.
- **Assigned WFH (seat cap)**: System-allocated WFH rows when on-site headcount would exceed `WFH_SEAT_CAP`. See [`docs/ASSIGNED_WFH.md`](docs/ASSIGNED_WFH.md) for the picker logic, exempt flag, swap mechanic, and dashboard ass/chair ratio.
- **Automatic cover assignment**: Covers assigned automatically when a team member goes on leave, using an independent R2 rotation for fairness.
- **Calendar subscriptions**: Personal ICS feeds per member with HAT, leave, approved WFH, holiday, and birthday VEVENTs (the last with `RRULE:FREQ=YEARLY`). Templated descriptions are overridable per event kind — see [Calendar template overrides](#calendar-template-overrides) below. HAT days covered by a swap suppress the per-member feed; the cover renders with `(COVER)`. Birthdays surface on every subscriber's feed; gate with `CALENDAR_BIRTHDAYS_ENABLED=false`.
- **Holiday support**: Holidays fetched from iCal URLs (`HOLIDAY_URLS`); scheduling skips them automatically.
- **Weekend/holiday awareness on the dashboard**: Status card header reads "Weekend" or "Holiday: <name>" with an Off tag; WFH override buttons hide with a "Next business day" note; schedule matrix shows "No support today" with the next business day.
- **No-one-WFH day marker**: When today has zero WFH rows and at least one person at work, an informational "No one is WFH today" banner appears with an 🏢 "All in" chip in the matrix column header. Mutually exclusive with the full-team banner (gold wins).
- **Birthday celebrations** ([issue #60](https://github.com/inful/madhatter/issues/60)): Optional birthdates trigger a 🎂 banner (any active birthday in the next 7 days) and a one-time confetti blast (exact day). Year is stored but never reaches the calendar. Gated by `BIRTHDAY_CONFETTI_ENABLED`, respects `prefers-reduced-motion: reduce`.
- **Fun effects on the dashboard** ([issue #59](https://github.com/inful/madhatter/issues/59)): Confetti burst when the full team is on-site, December snow, autumnal-equinox falling leaves. Each has its own operator gate (`CONFETTI_ENABLED`, `SNOW_ENABLED`, `LEAVES_ENABLED`). All respect `prefers-reduced-motion: reduce`.

### User Interface
- **Web dashboard**: HTMX-based responsive server-side-rendered UI (no SPA framework).
- **REST API**: HUMA-based, with auto-generated OpenAPI at `GET /docs`.
- **CLI tools**: Kong-based command-line interface for all operations.

### Authentication & Security
- **OAuth2 authentication**: Forgejo and GitLab providers, configured via per-provider env vars (`FORGEJO_*`, `GITLAB_*`).
- **Group-based access control**: Optional GitLab group/subgroup restriction via `GITLAB_ALLOWED_GROUP`.
- **Session management**: Secure token hashing (SHA-256) and encryption (AES-256-GCM).
- **Role-based access**: Admin and Regular. The **first user to sign in becomes admin automatically**; subsequent users land in a pending state and need admin approval from `/team/users` before they can sign in. Admins can promote, deactivate, or reactivate accounts.
- **Development mode**: Fake OAuth provider (`serve --development`) for local testing.
- **Per-IP rate limiting**: Token-bucket limiter on `/auth/login/{provider}` (10 req/min) and `/api/v1/tokens/*` (30 req/min). Returns `429` with `Retry-After`. Bucket sizes are hardcoded today — env-var overrides are planned but not wired.
- **Defensive HTTP response headers**: `Content-Security-Policy: default-src 'self'`, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: same-origin`; HTTPS adds `Strict-Transport-Security: max-age=63072000; includeSubDomains`. Third-party CSS/JS/fonts are vendored under `internal/web/assets/` so the strict CSP stays tight.

### Advanced Features
- **Automatic schedule maintenance**: 14-day rolling schedule; fills gaps but preserves existing assignments ("static as possible"). Triggers on team changes, leave reports, and page loads.
- **Static scheduling**: Existing assignments are preserved; only gaps are filled.
- **Event-driven updates**: Schedule maintenance runs on team changes, leave reports, and web requests — no manual cron.
- **Dual-mode generation**: `schedule generate` supports `fill gaps` (default) and `regenerate from scratch`.
- **Presence tracking**: Dashboard visual indicators for who's available, on leave, or WFH.
- **Work From Home (WFH)**: Ad-hoc requests plus contractual recurring weekdays. The settlement scheduler auto-approves or denies pending requests within a configurable window ahead. Same-day WFH via the dashboard button, `POST /api/v1/wfh/report-today`, or `wfh report <member-id>` CLI. The seat-cap picker, swap mechanic, quota rules, and withdrawal semantics are in [`docs/ASSIGNED_WFH.md`](docs/ASSIGNED_WFH.md); env vars are in [Configuration → Work From Home (WFH)](#work-from-home-wfh); the full design rationale is in [`plans/assigned-wfh-plan.md`](plans/assigned-wfh-plan.md).
- **Email notifications**: Team members are emailed on HAT-swap requests, WFH state changes, and cover assignments. One-click unsubscribe via HMAC-signed token in every email (RFC 8058 `List-Unsubscribe` headers). See [`docs/NOTIFICATIONS.md`](docs/NOTIFICATIONS.md).

## Quick Start

### Prerequisites
- Go 1.25 or later
- SQLite3 (included via github.com/ncruces/go-sqlite3)

### Installation
```bash
# Clone or download the project
cd madhatter

# Build the application. The ldflags inject the build identity
# into the running binary so the version reported in the footer
# of every page matches what goreleaser / your release pipeline
# shipped. Without ldflags the binary prints "dev" in the footer.
go build -ldflags "\
  -X github.com/inful/madhatter/internal/version.Version=v0.35.1 \
  -X github.com/inful/madhatter/internal/version.Commit=$(git rev-parse --short HEAD) \
  -X github.com/inful/madhatter/internal/version.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o support-rota

# Run tests
go test ./...
```

The footer at the bottom of every page renders `{{version}}` as
`v0.35.1` (or `v0.35.1+abc1234` when a Commit is set), so users can
see at a glance which build they are looking at when filing a bug.
GoReleaser is already wired in `.goreleaser.yaml` to inject these
on tag builds.

### Basic Usage

#### 1. Add Team Members
```bash
./support-rota team add "Alice Johnson" alice@example.com
./support-rota team add "Bob Smith" bob@example.com
./support-rota team add "Charlie Brown" charlie@example.com
```

#### 2. Start Web Server
```bash
# Production mode (requires OAuth configuration)
./support-rota serve --port 8080

# Development mode (no OAuth setup required)
./support-rota serve --port 8080 --development
```

#### 3. Access the Interface
- Navigate to `http://localhost:8080`
- The system automatically maintains a 14-day rolling schedule
- First user to login becomes admin automatically

#### 4. Report Leave
```bash
# Via CLI
./support-rota leave report alice@example.com <YYYY-MM-DD> <YYYY-MM-DD>

# Via Web Interface
# Navigate to /leave/report (requires login)
```

The CLI always records `LeaveTypeLeave`; for conference leave, use
the web form at `/leave/report`. The end date is inclusive.

#### 5. Create Calendar Subscription
```bash
# Via CLI
./support-rota calendar subscribe alice@example.com

# Via Web Interface
# Navigate to /calendar (requires login)
```

## Architecture Overview

### Technology Stack
- **Language**: Go 1.25
- **API Framework**: HUMA v2 with go-chi/chi router
- **Web Framework**: HTMX (server-side rendering)
- **Database**: SQLite (github.com/ncruces/go-sqlite3)
- **CLI Framework**: Kong
- **Authentication**: OAuth2 (Forgejo, GitLab)
- **Calendar Format**: ICS (iCalendar)

### Database Schema
```
team_members          - Team member records
leave_records         - Leave requests and status
rota_assignments      - Daily support assignments
calendar_subscriptions - User calendar subscriptions
users                 - OAuth user accounts
sessions              - User sessions (hashed tokens)
oauth_tokens          - Encrypted OAuth tokens
```

### Key Components

#### Schedule Engine (`internal/rota/engine.go`)
- Round-robin assignment algorithm
- Weekend and holiday skipping
- Cover assignment logic
- Independent R2 cover rotation for fairness

#### Schedule Maintenance (`internal/rota/maintenance.go`)
- `EnsureSchedule()` - Creates 14-day rolling schedule
- `GenerateMissingDays()` - Fills gaps while preserving assignments
- `RegenerateSchedule()` - Recreates schedule from scratch
- `HandleTeamChange()` - Updates schedule on team changes
- `HandleLeaveChange()` - Creates cover assignments

#### Holiday Service (`internal/holiday/`)
- Automatic fetching from iCal URLs
- Background scheduler (daily updates)
- Integration with schedule engine
- API endpoints for status and refresh

#### Authentication System (`internal/auth/`)
- OAuth2 provider abstraction
- Session management with SHA-256 hashing
- Token encryption with AES-256-GCM
- Role-based middleware

## Configuration

### Environment Variables

The application is primarily configured through environment variables.

#### Production-critical

| Variable | Default | Notes |
| --- | --- | --- |
| `SESSION_SECRET` | none | Required in production for session signing and validation. |
| `TOKEN_ENCRYPTION_KEY` | random ephemeral key | Optional for local development, but required in production if you want stored OAuth tokens to survive restarts. Must be a base64-encoded 32-byte key. |

#### OAuth provider configuration

At least one provider must be configured for production authentication.

| Variable | Default | Notes |
| --- | --- | --- |
| `FORGEJO_CLIENT_ID` | none | Enables Forgejo auth when set. |
| `FORGEJO_CLIENT_SECRET` | none | Forgejo OAuth client secret. |
| `FORGEJO_REDIRECT_URL` | none | Forgejo callback URL. |
| `FORGEJO_AUTH_URL` | `/login/oauth/authorize` | Override for self-hosted Forgejo. |
| `FORGEJO_TOKEN_URL` | `/login/oauth/access_token` | Override for self-hosted Forgejo. |
| `FORGEJO_USERINFO_URL` | `/api/v1/user` | Override for self-hosted Forgejo. |
| `FORGEJO_SCOPE` | `read:user` | Forgejo OAuth scope. |
| `GITLAB_CLIENT_ID` | none | Enables GitLab auth when set. |
| `GITLAB_CLIENT_SECRET` | none | GitLab OAuth client secret. |
| `GITLAB_REDIRECT_URL` | none | GitLab callback URL. |
| `GITLAB_AUTH_URL` | `https://gitlab.com/oauth/authorize` | Override for self-hosted GitLab. |
| `GITLAB_TOKEN_URL` | `https://gitlab.com/oauth/token` | Override for self-hosted GitLab. |
| `GITLAB_USERINFO_URL` | `https://gitlab.com/api/v4/user` | Override for self-hosted GitLab. |
| `GITLAB_SCOPE` | `read_user` | If `GITLAB_ALLOWED_GROUP` is set and `GITLAB_SCOPE` is unset, the effective default becomes `read_api read_user`. |
| `GITLAB_ALLOWED_GROUP` | none | Optional GitLab group/subgroup path restriction, for example `myorg/platform`. |

#### Work From Home (WFH)

| Variable | Default | Notes |
| --- | --- | --- |
| `WFH_ENABLED` | `true` | Enables the WFH feature. |
| `WFH_MIN_ONSITE_PERCENTAGE` | `50.0` | Percentage of active team members that must be on-site; rounded up before comparison with the absolute floor. |
| `WFH_MIN_ONSITE_ABSOLUTE` | `1` | Hard floor for the on-site minimum; the system uses whichever of this and the rounded-up percentage is higher. |
| `WFH_MAX_DAYS_PER_PERIOD` | `2` | Max WFH days per quota period, counting pending and approved requests and contractual recurring weekdays. |
| `WFH_PERIOD_DAYS` | `7` | Length of one WFH quota period. |
| `WFH_PERIOD_ANCHOR` | `2026-01-05` | Reference date used to compute WFH periods. Must use `YYYY-MM-DD`. |
| `WFH_SETTLEMENT_DAYS` | `7` | Number of days ahead that pending WFH requests are auto-settled. The default matches `WFH_PERIOD_DAYS` so a request submitted any time in the current period is settled by the next scheduler tick. |
| `WFH_REQUEST_HORIZON_DAYS` | `90` | Maximum number of days ahead a WFH request can be submitted. Requests beyond this horizon are rejected with a 422 in the API and a banner in the web form. |
| `WFH_PURGE_ENABLED` | `true` | When `true`, the daily scheduler hard-deletes `wfh_requests` rows whose date is strictly before the start of the previous quota period. The current and previous periods are always preserved. Opt out with `WFH_PURGE_ENABLED=false`. The same cutoff is exposed via `wfh purge [--apply]` and `/admin/wfh/purge`; both default to dry-run. |
| `WFH_SETTLEMENT_INTERVAL` | `15m` | Period between settlement scheduler ticks (Go duration format, e.g. `5m`, `1h`, `30s`). Lower values reduce the perceived latency between a request submission and the approve/deny decision; higher values save on CPU. |
| `WFH_SEAT_CAP` | _unset_ | Hard seat cap on the office. When set, the settlement scheduler inserts system-allocated **Assigned** WFH rows for any day where the on-site headcount would exceed the cap. Picks prefer members with the fewest voluntary WFHs in the period, then a co-presence tiebreaker, then alphabetical. See [`docs/ASSIGNED_WFH.md`](docs/ASSIGNED_WFH.md) for the full reference. |
| `WFH_ASSIGNMENT_ENABLED` | `true` | Master switch for the seat-cap picker. Setting this to `false` leaves the cap in the dashboard math but disables the picker so no `Assigned` rows are written. |
| `WFH_COPRESENCE_ENABLED` | `true` | Master switch for the co-presence tiebreaker. When `false`, the picker falls back to "fewest voluntary WFHs in the period, then alphabetical". |
| `WFH_COPRESENCE_HORIZON_DAYS` | `14` | Calendar days the co-presence scanner looks back when scoring members for a pick. |
| `WFH_COPRESENCE_RETENTION_DAYS` | `30` | How long the co-presence history rows are kept in `wfh_co_presence` before they're pruned. |

#### Dashboard

| Variable | Default | Notes |
| --- | --- | --- |
| `HAT_LINK_URL` | none | URL the HAT day badge in the dashboard Today card links to (opens in a new window via `target="_blank" rel="noopener"`). When unset, the badge renders as a plain `<span>` (the original behavior). Useful for an on-call runbook, a Slack channel, or a PagerDuty rotation page. |
| `CONFETTI_ENABLED` | `true` | Enables the celebratory confetti burst on the dashboard when the entire team is on-site on a business day. The burst uses the vendored [canvas-confetti](https://github.com/catdad/canvas-confetti) library and respects `prefers-reduced-motion`. |
| `SNOW_ENABLED` | `true` | Enables a gently-falling snow storm on the dashboard throughout December. Uses the vendored canvas-confetti library; snow short-circuits entirely when `prefers-reduced-motion: reduce` is set. |
| `LEAVES_ENABLED` | `true` | Enables a storm of falling leaf emojis (🌿 herb, 🍁 maple, 🍂 fallen, 🍃 wind-fluttering) on the dashboard on the autumnal equinox (September 23) when the entire team is on-site on a business day. Uses `confetti.shapeFromText` to rasterise the emojis into particle sprites; respects `prefers-reduced-motion`. |
| `BIRTHDAY_CONFETTI_ENABLED` | `true` | Enables the birthday confetti blast on the dashboard when a member has a birthday today (issue #60 follow-up). The blast fires from the top corners with pink + gold particles interleaved with 🎂 cake and 💗 heart shapes. Distinct from the full-team confetti burst (different origin, smaller particle count, distinct shape set); both can fire on the same day. Respects `prefers-reduced-motion: reduce`. |
| `CALENDAR_BIRTHDAYS_ENABLED` | `true` | Emits one `🎂 <member>'s birthday` all-day VEVENT per active member whose birthdate is set, with `RRULE:FREQ=YEARLY` so the event recurs every year on the calendar client (issue #60 follow-up). The year of birth never reaches the .ics output — only MM-DD lands in the SUMMARY. Set to `false` to disable the feature without touching the other event kinds (HAT / leave / WFH / holiday). |

#### Calendar and meetings

| Variable | Default | Notes |
| --- | --- | --- |
| `MEETINGS_TIMEZONE` | `Europe/Oslo` | Used when generating meeting events. Invalid values fall back to `Europe/Oslo`. |
| `MEETINGS_TEAMS_URL` | none | Teams join URL included in meeting events. |
| `MEETINGS_TEMPLATE_TEXT_PATH` | built-in template | Optional text/template override for meeting descriptions. |
| `MEETINGS_TEMPLATE_HTML_PATH` | built-in template | Optional html/template override for meeting descriptions. |
| `MEETINGS_LINKS` | none | Comma-separated shared links for meeting events. |
| `MEETINGS_LINKS_MORNING` | falls back to `MEETINGS_LINKS` | Overrides links for Tue-Fri morning shuffle meetings. |
| `MEETINGS_LINKS_PROJECT` | falls back to `MEETINGS_LINKS` | Overrides links for Monday project shuffle meetings. |
| `SUPPORT_DAY_LINKS` | none | Extra links included in support-duty calendar events. |
| `SUPPORT_DAY_SHUFFLE_SEED` | `support-rota-presence` | Salt for the per-day stable randomisation in support/leave/holiday templates. |
| `SUPPORT_ASSIGNMENT_TEMPLATE_TEXT_PATH` | built-in template | Optional text/template override for support assignment descriptions. |
| `SUPPORT_ASSIGNMENT_TEMPLATE_HTML_PATH` | built-in template | Optional html/template override for support assignment descriptions. |
| `LEAVE_TEMPLATE_TEXT_PATH` | built-in template | Optional text/template override for leave event descriptions. |
| `LEAVE_TEMPLATE_HTML_PATH` | built-in template | Optional html/template override for leave event descriptions. |
| `HOLIDAY_TEMPLATE_TEXT_PATH` | built-in template | Optional text/template override for holiday descriptions. |
| `HOLIDAY_TEMPLATE_HTML_PATH` | built-in template | Optional html/template override for holiday descriptions. |

#### Holidays and database

| Variable | Default | Notes |
| --- | --- | --- |
| `HOLIDAY_URLS` | none | Comma-separated holiday iCal feed URLs. If unset, holiday support is effectively disabled. |
| `MIGRATIONS_PATH` | auto-detected | Optional absolute or relative path to the migrations directory. If unset, the app searches common repo-relative locations. |

#### Notifications

The notification subsystem (`internal/notify/`) is configured entirely
from env vars. The two design knobs are **enable email delivery** and
the **per-event template overrides**; see [`docs/NOTIFICATIONS.md`](docs/NOTIFICATIONS.md)
for the full reference (what fires when, the unsubscribe flow, the
"add a new channel" checklist, ops queries for the outbox).

| Variable | Default | Notes |
| --- | --- | --- |
| `NOTIFY_EMAIL_ENABLED` | `false` | When `true`, the email channel is registered. Required for any email delivery. |
| `NOTIFY_SMTP_HOST` | _none_ | `host:port` of the SMTP server. Required when email is enabled. |
| `NOTIFY_SMTP_FROM` | `MadHatter Rota <noreply@example.com>` | The `From:` address (display name + email). |
| `NOTIFY_BASE_URL` | `http://localhost:8080` | Used in templates for "view in dashboard" links. |
| `NOTIFY_PUBLIC_BASE_URL` | _falls back to `NOTIFY_BASE_URL`_ | Externally-visible origin for absolute URLs in emails (one-click unsubscribe links). Should be the public HTTPS host users actually visit. |
| `NOTIFY_OUTBOX_POLL_INTERVAL` | `30s` | How often the outbox worker checks for due rows. |
| `NOTIFY_OUTBOX_MAX_ATTEMPTS` | `5` | After this many failures an outbox row is marked `dead`. |
| `NOTIFY_OUTBOX_BACKOFF_BASE` | `30s` | First retry delay. Subsequent retries double, capped at 1h. |

#### Rate limiting

Per-IP token-bucket throttles (see [`internal/ratelimit/`](internal/ratelimit/))
protect the OAuth initiation route and the API token endpoints. The
default bucket sizes are **10 req/min for `/auth/login/{provider}`**
(constants `defaultAuthRateLimit` / `defaultAuthRateRefill` in
[`internal/web/handler.go`](internal/web/handler.go)) and **30 req/min
for `/api/v1/tokens/*`** (constants `defaultTokenRatePerIP` /
`defaultTokenRateRefillS` in [`internal/api/server.go`](internal/api/server.go)).
Excessive requests get a `429` with a `Retry-After` header. These
values are hardcoded today — operator overrides via env vars are
[planned but not wired](https://github.com/inful/madhatter/blob/main/API_AUTH_IMPLEMENTATION.md#roadmap).

Example production setup:

```bash
export SESSION_SECRET="$(openssl rand -base64 32)"
export TOKEN_ENCRYPTION_KEY="$(openssl rand -base64 32)"

export GITLAB_CLIENT_ID="your-client-id"
export GITLAB_CLIENT_SECRET="your-client-secret"
export GITLAB_REDIRECT_URL="https://your-domain/auth/callback?provider=gitlab"

export HOLIDAY_URLS="https://www.officeholidays.com/subscribe/norway"

export WFH_MIN_ONSITE_PERCENTAGE="50"
export WFH_MAX_DAYS_PER_PERIOD="2"
```

Notes:

- `HOLIDAY_FETCH_INTERVAL` and `HOLIDAY_LOOKAHEAD` are not currently read from environment variables by the application.
- Meeting template and link environment variables are read when calendar output is generated.
- Most other environment variables are loaded during server startup.

### Calendar template overrides

Every calendar event description is rendered through a Go template so deployments can tailor the wording for their team. Two templates are supported per event kind — a `text/template` for the iCalendar `DESCRIPTION` and an `html/template` for the `X-ALT-DESC` (Outlook-friendly). Built-in defaults reproduce the project's hard-coded output, so the templates are entirely opt-in.

#### Environment variables

| Event kind | Text template env var | HTML template env var |
| --- | --- | --- |
| Meeting (morning/project) | `MEETINGS_TEMPLATE_TEXT_PATH` | `MEETINGS_TEMPLATE_HTML_PATH` |
| Support assignment | `SUPPORT_ASSIGNMENT_TEMPLATE_TEXT_PATH` | `SUPPORT_ASSIGNMENT_TEMPLATE_HTML_PATH` |
| Leave | `LEAVE_TEMPLATE_TEXT_PATH` | `LEAVE_TEMPLATE_HTML_PATH` |
| Holiday | `HOLIDAY_TEMPLATE_TEXT_PATH` | `HOLIDAY_TEMPLATE_HTML_PATH` |
| Birthday (#60 follow-up) | `BIRTHDAY_TEMPLATE_TEXT_PATH` | `BIRTHDAY_TEMPLATE_HTML_PATH` |

Setting any of these to a non-existent or syntactically broken file surfaces a 500-style error on the next calendar request. Leave them unset to keep the built-in defaults.

`SUPPORT_DAY_SHUFFLE_SEED` (default `support-rota-presence`) is the salt for the per-day stable randomisation in support, leave, and holiday templates. Change it to decouple those orderings from each other and from the meetings agenda shuffle.

#### The presence snapshot

Support, leave, and holiday templates all share one piece of data: the per-day **presence snapshot** for the event's date. The snapshot is computed once per day per request from the database, so every event rendered for the same date sees identical data. A template can use any of the following fields:

| Field | Type | Notes |
| --- | --- | --- |
| `Date` | `string` | The event's date, `"2006-01-02"`. |
| `IsWeekend` | `bool` | True for Saturday or Sunday. |
| `IsHoliday` | `bool` | True when a holiday is configured for the date. |
| `HolidayName` | `string` | The holiday's name, or empty. |
| `TotalActive` | `int` | Number of active team members. |
| `OnSite` | `[]struct` | Active members not on leave and not WFH. Each entry has `ID`, `Name`, `Email`. |
| `OnLeave` | `[]struct` | Active members on leave that day. |
| `WFH` | `[]struct` | Active members with an approved WFH that day, including materialised recurring-WFH rows. |
| `HATName` | `string` | The on-call (HAT) member's name, or empty. |
| `HATIsCover` | `bool` | True when the on-call is covering for someone else. |
| `HATMemberID` | `string` | The on-call member's ID. |
| `ShuffledOrder` | `[]struct` | Stable, per-day random order of present members (driven by `SUPPORT_DAY_SHUFFLE_SEED`). |

The same fields are available on every event kind because the data structs embed the snapshot. A holiday template that prints "Office closed — 5 people on-site, 2 on leave, 1 WFH" can do so with one template, and the same template can be reused for the support day to show the day's on-site count.

The WFH count includes materialised recurring-WFH rows. The materialiser runs once per day per request, so a calendar request that hits a date the WFH feature hasn't materialised yet will see a smaller WFH count; the next calendar request (after a WFH list page load) is correct.

#### Per-event data fields

**Support assignment** — `Summary`, `BaseText` (`"Support duty"` plus optional `"(cover)"` / `" (cover) for leave"`), `IsCover`, `IsCoverForLeave`, `Date`, `Links` (same shape as meetings).

**Leave** — `Summary`, `BaseText` (`"{LeaveType} leave for {MemberName}"`), `MemberID`, `MemberName`, `LeaveType`, `StartDate`, `EndDate`.

**Holiday** — `Summary` (`"Office Closed - {Name}"`), `BaseText` (`"Support rota is not scheduled on this day"`), `Name`, `Date`.

**Meeting** (for completeness) — `MeetingName`, `TeamsURL`, `Links`, `Present` (`[]string`), `Away` (`[]string`), `Support`, `Shuffle` (`[]string`), `Agenda` (`[]string`).

#### HTML helpers

The HTML templates for support, leave, and holiday events have two helper functions pre-registered: `{{htmlHeading "Title"}}` produces `<h3>Title</h3>`, and `{{htmlParagraph "Body"}}` produces `<p>Body</p>`. Custom templates can call them, and the built-in defaults use them too.

#### Example: support-day runbook

A deployment wants the support event to include the team's HAT-day runbook, the day-of HAT name, and a quick stats block. Save the following as a file the `SUPPORT_ASSIGNMENT_TEMPLATE_TEXT_PATH` env var points at.

```gotemplate
{{.Summary}}

Runbook: https://runbooks.example.com/hat-day

Day stats: {{.TotalActive}} active, {{len .OnSite}} on site, {{len .OnLeave}} on leave, {{len .WFH}} WFH.
Today's HAT: {{.HATName}}{{if .HATIsCover}} (cover){{end}}.
```

The same data drives a richer HTML version. Save this as the `SUPPORT_ASSIGNMENT_TEMPLATE_HTML_PATH` target.

```gotemplate
{{htmlHeading .Summary}}
<p>Runbook: <a href="https://runbooks.example.com/hat-day">HAT-day runbook</a></p>

<h4>Day stats</h4>
<ul>
  <li>Active: {{.TotalActive}}</li>
  <li>On site: {{len .OnSite}}</li>
  <li>On leave: {{len .OnLeave}}</li>
  <li>WFH: {{len .WFH}}</li>
</ul>

{{if .HATName}}
<h4>Today's HAT</h4>
<p>{{.HATName}}{{if .HATIsCover}} (cover){{end}}</p>
{{end}}

{{if .IsHoliday}}
<p><em>Office is closed today for {{.HolidayName}}.</em></p>
{{end}}
```

#### Example: holiday template with coverage

A deployment wants the holiday event to list who would normally be on site.

```gotemplate
Office closed: {{.Name}}

If we were open today, we'd have {{len .OnSite}} on site, {{len .OnLeave}} on leave, and {{len .WFH}} WFH.
Stable order for the day: {{range .ShuffledOrder}}{{.Name}} {{end}}
```

#### Example: leave template

A deployment wants the leave event to include a back-to-work reminder.

```gotemplate
{{.BaseText}}

Back-to-work checklist: https://handbook.example.com/back-to-work
```

#### Notes

- The HTML template should output a HTML fragment; the calendar library wraps it in `<html><body>...</body></html>`.
- Long lines in iCalendar files are folded per RFC 5545. This is normal.
- `MEETINGS_LINKS` and `SUPPORT_DAY_LINKS` are trusted deployment input. If you use raw HTML anchors, they are included as-is.
- If `MEETINGS_LINKS_PROJECT` / `MEETINGS_LINKS_MORNING` are set, they override `MEETINGS_LINKS` for their respective events.

### Development Mode
For local development without OAuth setup:
```bash
./support-rota serve --port 8080 --development
```
This uses a fake OAuth provider that automatically creates an admin user.

## API Reference

The full API surface is published as an interactive OpenAPI document at `GET /docs` (HUMA generates it from `internal/api/operations.go`, which is the source of truth). Every `/api/v1/*` operation except the holidays endpoints and the per-token calendar feeds requires an authenticated user — see [API Authentication](#api-authentication) below for how to obtain a bearer token.

The table below summarises the route groups; the OpenAPI doc has the exact request/response shapes and admin requirements.

| Route group | Path prefix | Auth | Notes |
| --- | --- | --- | --- |
| Team | `/api/v1/team` | session or bearer | Full CRUD; write paths require admin. |
| Leave | `/api/v1/leave` | session or bearer | Full CRUD; write paths require admin. |
| Schedule | `/api/v1/schedule/generate` | bearer, admin | Regenerate the schedule for a date range. |
| Calendar | `/api/v1/calendar/subscribe` | session or bearer | Create a personal ICS subscription. |
| WFH | `/api/v1/wfh` | session or bearer | Full WFH lifecycle (ad-hoc, recurring, assigned, swap). |
| Swaps | `/api/v1/swaps` | session or bearer | HAT-day swap requests (create / list / accept / reject / cancel / admin-delete). |
| API tokens | `/api/v1/tokens` | session or bearer | Generate / list / revoke bearer tokens. |
| Holidays | `/api/v1/holidays` | **public** | The only public `/api/v1/*` endpoints. |
| Presence | `/api/v1/presence/today` | session or bearer | Today's on-site / leave / WFH roster. |

### API Authentication

Every protected `/api/v1/*` endpoint accepts either the `session_token` cookie (same as the web UI) or `Authorization: Bearer <token>`. Generate a token with the web UI (User menu → API tokens) or directly:

```bash
curl -X POST http://localhost:8080/api/v1/tokens/generate \
  -H "Cookie: session_token=<session-cookie>" \
  -H "Content-Type: application/json" \
  -d '{"name": "my-cli-token", "expires_in_days": 30}'
```

The plaintext token is shown once. List your tokens with `GET /api/v1/tokens` and revoke with `DELETE /api/v1/tokens/{id}`. See [`API_AUTH_IMPLEMENTATION.md`](API_AUTH_IMPLEMENTATION.md) for the design notes and security model.

### OpenAPI Documentation

- `GET /docs` - Interactive OpenAPI documentation (auto-generated by HUMA from `internal/api/operations.go`).

## Web Interface

The full route table is in [`internal/web/routes.go`](internal/web/routes.go) — that's the source of truth and changes whenever a route is added. The summary below groups the routes by role; the registration table in `routes.go` has the exact path, method, and middleware for each one.

### Public

- `/` - Dashboard (the only public route in production; shows today's roster and upcoming presence)
- `/login` - Login page (redirects to OAuth provider)
- `/auth/login/{provider}`, `/auth/callback`, `/auth/logout` - OAuth flow
- `/calendar/{token}/ics`, `/calendar/{token}/team.ics`, `/calendar/{token}/meetings.ics`, `/calendar/{token}/meetings/{date}.html` - per-member calendar feeds (token in URL is the auth)
- `/help` - In-app help and env-var reference
- `/unsubscribe`, `/unsubscribe/resume` - one-click email unsubscribe (HMAC token in the URL is the auth)

### Authenticated (any user)

- `/leave/report`, `/leave/report-sick`, `/leave/manage`, `/leave/{id}/edit`, `/leave/{id}/delete`
- `/calendar` - personal subscription management
- `/swaps`, `/swaps/{id}/{accept,reject,cancel}` - HAT-swap requests
- `/wfh`, `/wfh/request`, `/wfh/report-today`, `/wfh/today/on-site`, `/wfh/on-site` - WFH self-service
- `/wfh/{id}/{cancel,withdraw,swap}`, `/wfh/swap/inbox`, `/wfh/swap/{swapId}/{accept,reject,cancel}` - WFH swap flow

### Admin

- `/team` (list/add), `/team/{id}/{edit,recurring-wfh,permanent-wfh,exempt,delete}` - team management
- `/team/users/{id}/{admin,approve,deny,deactivate,reactivate}` - user approval and role management (the first user becomes admin automatically; subsequent users need approval from an existing admin before they can sign in)
- `/schedule/generate` - regenerate the schedule
- `/admin/database/backup`, `/admin/database/restore` - database backup and restore
- `/calendar/subscriptions`, `/calendar/subscriptions/cleanup` - manage all subscriptions
- `/admin/wfh`, `/admin/wfh/{id}/{withdraw,reassign,unmark}`, `/admin/wfh/{settle,purge,mark}` - WFH admin and lifecycle
- `/swaps/{id}/delete` - admin-delete a swap

### Features
- **Dashboard**: Shows today's assignment, upcoming presence, current/next week, holidays
- **Team Management**: Add/remove team members, triggers schedule updates
- **Leave Reporting**: Unified leave types with automatic cover assignment
- **Schedule Generation**: Dual-mode (fill gaps vs. regenerate)
- **Calendar Subscriptions**: Copy button with clipboard API and visual notifications

## CLI Commands

### Server
```bash
./support-rota serve --port 8080
./support-rota serve --port 8080 --development
./support-rota serve --port 8080 --reassign-covers=false   # skip the startup cover-reassignment
```

### Team Management
```bash
./support-rota team add "Name" email@example.com
./support-rota team list
```

### Leave Management
```bash
# Always records LeaveTypeLeave; for conference leave use the web form.
./support-rota leave report email@example.com <YYYY-MM-DD> <YYYY-MM-DD>
./support-rota leave list
```

### Schedule
```bash
./support-rota schedule generate <YYYY-MM-DD> <YYYY-MM-DD>
./support-rota schedule view <YYYY-MM-DD>
```

### Calendar
```bash
./support-rota calendar subscribe email@example.com
./support-rota calendar export email@example.com output.ics
```

### WFH Management
```bash
# Dry-run by default; prints the cutoff and how many rows would be deleted.
./support-rota wfh purge

# Commit the deletion.
./support-rota wfh purge --apply

# One-off catch-up clean with a custom cutoff.
./support-rota wfh purge --before <YYYY-MM-DD> --apply

# Report WFH for today (settled inline against the on-site floor).
./support-rota wfh report <member-id-or-email>
```

The cutoff defaults to the start of the previous quota period (computed from `WFH_PERIOD_ANCHOR` and `WFH_PERIOD_DAYS`). The same operation is exposed at `GET /admin/wfh/purge` for a preview and `POST /admin/wfh/purge` to commit from the web UI. Errors with `WFH feature is disabled` when `WFH_ENABLED=false`.

### Database backup and restore (CLI)

Mirrors the web UI's `/admin/database/backup` and `/admin/database/restore` so scripted operators (cron, systemd timers, ansible) can snapshot and restore without going through a browser.

```bash
# Take a consistent SQLite snapshot to <path>. Refuses to overwrite
# an existing file; pass --force to clobber.
./support-rota backup /var/backups/support-rota-$(date -F).db

# Validate a candidate backup without mutating the live database.
# Same validation the web UI runs in the review step before apply.
./support-rota restore /var/backups/support-rota-2026-09-30.db

# Validate, then commit. Re-runs the same validation gate the web
# UI applies before flipping to the apply step; a failed validation
# aborts without touching state.
./support-rota restore /var/backups/support-rota-2026-09-30.db --apply
```

`backup` produces a 0o600 SQLite snapshot via the same `VACUUM INTO` the web handler uses — the file is a drop-in replacement for the one downloaded from `/admin/database/backup`. `restore` reads the candidate, validates against the live schema, and (with `--apply`) commits. By default it validates only, so an operator can dry-run before committing. Empty input is refused with a clear error before any database method is dispatched. Restore is capped at 50 MB to match the web handler's upload ceiling.

### Cover reassignment

`reassign-covers` re-runs the cover-assignment algorithm against every leave in the database. The operation is idempotent on a steady-state rota, so it's safe to invoke at any time — including to recover from manual cover edits or to confirm a deploy. The same logic also runs automatically on every `serve` startup unless `--reassign-covers=false` is passed.

```bash
# Run on demand.
./support-rota reassign-covers
```

### HAT Swap Repair

`swap reconcile` is the historical-repair path for swaps accepted before v0.32.5 (which used the captured swap pair) and v0.32.3 (which protected swap-set rows from the cover scheduler). It rewrites `rota_assignments.member_id` and `is_swapped` on both sides of an accepted swap so the assignments match the swap record. Future swaps already use the new code paths; this command is for repairing the historical state.

```bash
# Dry-run a single swap by ID — reports drift without mutating.
./support-rota swap reconcile --id=<swap-id>

# Commit the reconciliation.
./support-rota swap reconcile --id=<swap-id> --apply

# Walk every accepted swap in the database (dry-run by default).
./support-rota swap reconcile --all

# Commit every drifted swap in one pass.
./support-rota swap reconcile --all --apply
```

The output lists each swap with a per-side drift report (`member_id: old → new`, `is_swapped: old → new`, or `<none>` if the row already matches). The command is idempotent — re-running on an already-reconciled swap produces no drift. `--id` and `--all` are mutually exclusive.

### Migration Status

`migrate status` reports the migration state of `support_rota.db` *without* applying pending migrations — useful when a deployment is suspected to have missed a migration, or when a schema is reported dirty. Migrations still auto-apply on every `serve` startup via `database.New`; this command is read-only and works even when the database refuses to advance (e.g., a dirty `schema_migrations` row).

```bash
./support-rota migrate status
```

Sample output on a healthy database:

```
Database: support_rota.db
  Applied version: 29
  Dirty:           false
  Latest on disk:  29
  Pending:         0
```

When the database is dirty the command prints a recovery hint with the SQL to clear the flag:

```
Database: support_rota.db
  Applied version: 24
  Dirty:           true
  Latest on disk:  29
  Pending:         0

WARNING: the database is in a dirty state. golang-migrate
refuses to advance past a dirty schema; inspect the failing
migration manually, then clear the flag with:
  UPDATE schema_migrations SET dirty = 0;
```

## Development

### Code Quality Standards
- **Linter**: golangci-lint v2 with 0 issues allowed
- **Cyclomatic Complexity**: Maximum 10 per function
- **Formatting**: gofumpt compliant
- **Comments**: All comments end with periods (godot)
- **Tests**: testify assertions (testifylint compliant)

### Running Tests
```bash
# All tests
go test ./... -v -cover

# Specific package
go test ./internal/database -v

# With race detector
go test -race ./...
```

### SQLC Generation
```bash
# Generate type-safe SQL code
export PATH=$PATH:$(go env GOPATH)/bin
sqlc generate

# Run database tests
go test ./internal/database -v
```

### Linting
```bash
# Check for issues
golangci-lint run

# Auto-fix issues
golangci-lint run --fix
```

## Testing

### Test Coverage
The Support Rota System maintains comprehensive test coverage across all components:

- **Database Layer**: 95%+ coverage with dynamic date handling
- **Authentication**: 90%+ coverage including OAuth flows and session management
- **Rota Engine**: 100% coverage for scheduling algorithms
- **API Handlers**: 85%+ coverage for all endpoints
- **Web Handlers**: 80%+ coverage for UI functionality

### Test Improvements
Recent comprehensive test coverage work included:

1. **Fixed Compilation Errors**: Resolved unused imports, invalid struct fields, and duplicate functions in `handlers_test.go`
2. **Fixed Test Logic**: Updated provider configurations, URL patterns, and error message assertions
3. **Dynamic Date Handling**: Converted all hardcoded dates to use relative dates based on `time.Now()` to prevent test failures
4. **Foreign Key Constraints**: Added proper user creation before session creation to satisfy constraints
5. **Variable Naming**: Improved descriptive variable names in complex test scenarios
6. **Security Fixes**: Added bounds checking to prevent potential security issues

### Running Specific Tests
```bash
# All tests with coverage
go test ./... -v -cover

# Auth package tests
go test ./internal/auth -v

# Database package tests
go test ./internal/database -v

# Rota package tests
go test ./internal/rota -v

# Web package tests
go test ./internal/web -v

# Race detector on all tests
go test -race ./...
```

### Test Quality Standards
- All tests use testify assertions (`require.NoError`, `assert.Equal`)
- Testifylint compliant
- Co-located with source files
- Dynamic dates to prevent brittleness
- All error paths tested
- Foreign key constraints properly handled

## Deployment

### Database backup and restore

Admin users can take a live SQLite snapshot from the web UI:

- `GET /admin/database/backup` — downloads a `.db` file copy of the running database (the route holds a short lock; large databases may briefly block reads).
- `GET /admin/database/restore` — upload a previously downloaded `.db`. The route validates compatibility (current migration version, schema match) and shows a diff before any apply.
- `POST /admin/database/restore` — applies the uploaded `.db` (irreversible — keep a fresh backup first).

The same operations are also exposed on the CLI for scripted operators — see [Database backup and restore (CLI)](#database-backup-and-restore-cli):

```bash
./support-rota backup /var/backups/support-rota-$(date +%F).db
./support-rota restore /var/backups/support-rota-$(date +%F).db --apply
```

For scripted backups without the binary on the server, copy the file out of band while the server is stopped, or use SQLite's `.backup` command while it's running:

```bash
sqlite3 support_rota.db ".backup /var/backups/support-rota-$(date +%F).db"
```

All three paths produce a file you can drop onto the restore page (web UI or CLI).

### Single Binary
```bash
go build -o support-rota
./support-rota serve --port 8080
```

### Docker
```dockerfile
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o support-rota

FROM alpine:latest
COPY --from=builder /app/support-rota /usr/local/bin/
EXPOSE 8080
CMD ["support-rota", "serve", "--port", "8080"]
```

### Systemd Service
```ini
[Unit]
Description=Support Rota System
After=network.target

[Service]
Type=simple
User=supportrota
WorkingDirectory=/opt/support-rota
ExecStart=/opt/support-rota/support-rota serve --port 8080
Restart=always

[Install]
WantedBy=multi-user.target
```

## Security Considerations

### Session Management
- Tokens are hashed with SHA-256 before storage
- Sessions expire after 24 hours by default
- Secure cookies with HttpOnly and SameSite flags

### OAuth Tokens
- Encrypted with AES-256-GCM before storage
- Require `TOKEN_ENCRYPTION_KEY` environment variable
- **API access uses a separate token system** (the `api_tokens` table). OAuth tokens are stored for the lifetime of the user's session in case a future feature needs to call the upstream provider's API on their behalf; they are not consumed by the support-rota API. Generate an API token via the web UI (User menu → API tokens) or `POST /api/v1/tokens/generate`. See [API Authentication](#api-authentication).

### Database
- Foreign keys must be enabled manually
- File permissions should be set to 600
- Regular backups recommended

### Production Checklist
- [ ] Use HTTPS for all connections
- [ ] Set strong `SESSION_SECRET`
- [ ] Set `TOKEN_ENCRYPTION_KEY` for OAuth token encryption
- [ ] Configure OAuth providers with exact callback URLs
- [ ] Restrict access to OAuth provider base URLs
- [ ] Set appropriate file permissions on database
- [ ] Enable regular database backups

## Troubleshooting

### Common Issues

#### "No OAuth providers configured"
**Solution**: Set at least one provider's environment variables or use `--development` flag

#### "Invalid redirect URI"
**Solution**: Ensure `app_url` in config matches your actual domain

#### "Failed to decrypt access token"
**Solution**: Ensure `TOKEN_ENCRYPTION_KEY` is set and consistent across restarts

#### Schedule gaps
**Solution**: Check that team members are active and dates are business days

#### Calendar subscription not working
**Solution**: Verify token exists in database and user has assignments

## Documentation

A navigable map of every Markdown file in this repo — one-line descriptions, audience, last-touched dates, and accuracy status — lives in [DOCS_INDEX.md](DOCS_INDEX.md). Start there if you don't know which doc you need.

## Support

For issues or questions:
1. Check [DOCS_INDEX.md](DOCS_INDEX.md) — the docs index lists where to look for each topic.
2. Check the logs for error messages
3. Verify configuration syntax
4. Test OAuth flow manually
5. Check database schema matches expected structure

## License

[Add license information here]

## Contributing

[Add contribution guidelines here]