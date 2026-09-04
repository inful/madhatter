# Support Rota System

A comprehensive support duty management system with automatic scheduling, leave management, calendar subscriptions, and OAuth2 authentication.

## Features

### Core Functionality
- **Round-robin scheduling**: Automatic assignment of support duties with fair distribution
- **Leave management**: Unified system for sick leave, vacation, and other absences. Each record carries a `leave_type` tag (Leave or Conference) for differentiated UI on the dashboard and management page. Non-admin users can register same-day leave on behalf of a colleague via the "Someone Called In Sick" form (`GET|POST /leave/report-sick`) — the date is pinned to today, a duplicate row for the same person and day is refused, and the form lives in the user menu under Leave.
- **Assigned WFH (seat cap)**: When the day's on-site headcount would exceed the configured `WFH_SEAT_CAP`, the seat-cap picker inserts system-allocated Assigned WFH rows for the excess members. The picker prefers members with the fewest voluntary WFHs in the period, falls back to a co-presence tiebreaker (members who haven't recently been on-site with the cohort are kept on-site so the team meets in person), and resolves ties alphabetically. Assigned WFHs appear on the WFH list with a yellow **Assigned** badge, cannot be self-withdrawn (use the **Request swap** button instead), and don't burn the voluntary quota. The dashboard's Today card surfaces the live headcount vs. cap as an **ass/chair ratio** ("X of Y chairs (N%)") with a green/orange/red progress bar so the picker state is visible without opening the admin page. Admins can flag any member as **exempt from assignment** from the team-edit form (a per-member `is_exempt_from_assignment` flag) — exempt members are never picked for involuntary WFH but can still volunteer via swap. Other members can swap into an Assigned WFH on a per-day basis; the cap stays met across the swap because the original Assigned row flips to **withdrawn** and a new row with `origin=swap` lands for the target. See [`docs/ASSIGNED_WFH.md`](docs/ASSIGNED_WFH.md) for the full user-facing and configuration reference.
- **Automatic cover assignment**: System automatically assigns covers when team members are on leave
- **Calendar subscriptions**: Personal ICS calendar URLs for any calendar app. Each member's per-user feed renders their HAT-day assignments as VEVENTs and their own approved WFH days as separate all-day VEVENTs titled `<member> - WFH` so the calendar stays accurate without consulting the dashboard. Admin-marked WFH days carry a "(marked by admin)" note in the event description. Only approved WFH rows surface on the calendar — pending, denied, and withdrawn rows do not. The event text and HTML are overridable per event kind via the standard template-override env vars (`CALENDAR_TEMPLATE_*`).
- **Holiday support**: Automatic exclusion of holidays from scheduling (via iCal feeds)

### User Interface
- **Web dashboard**: HTMX-based responsive user interface
- **REST API**: HUMA-based API with automatic OpenAPI documentation
- **CLI tools**: Kong-based command-line interface for all operations

### Authentication & Security
- **OAuth2 authentication**: Support for Forgejo and GitLab providers
- **Group-based access control**: Optional GitLab group membership validation
- **Session management**: Secure token hashing (SHA-256) and encryption (AES-256-GCM)
- **Role-based access**: Admin and Regular user roles
- **Development mode**: Fake OAuth provider for local testing
- **Per-IP rate limiting**: Token-bucket limiter on `/auth/login/{provider}` (10 req/min default) and `/api/v1/tokens/*` (30 req/min default). Excessive requests get a 429 with a `Retry-After` header. Set `WFH_SETTLEMENT_DAYS` and the bucket size per route in production.
- **Defensive HTTP response headers**: Every response carries a strict `Content-Security-Policy` (`default-src 'self'` with no external exceptions), `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, and `Referrer-Policy: same-origin`. HTTPS requests additionally get `Strict-Transport-Security: max-age=63072000; includeSubDomains`. The CSP can stay tight because all third-party CSS/JS/fonts (HTMX, Bulma, FontAwesome) are vendored under `internal/web/assets/` and served from `/static/*`.

### Advanced Features
- **Automatic schedule maintenance**: 14-day rolling schedule maintained automatically
- **Static scheduling**: Preserves existing assignments, only fills gaps
- **Event-driven updates**: Schedule updates triggered on team changes, leave reports
- **Dual-mode generation**: Fill gaps vs. regenerate from scratch
- **Presence tracking**: Visual indicators for who's available and on leave
- **Work From Home (WFH)**: Ad-hoc requests plus contractual recurring weekdays. The settlement scheduler auto-approves or denies pending requests within a configurable window ahead. A "WFH today" entry point (dashboard button, `POST /api/v1/wfh/report-today`, CLI `wfh report <member-id>`) lets a member report same-day WFH for unforeseen circumstances — the request is created and settled inline so the dashboard reflects the outcome within the same request, no waiting for the next tick. Capacity-floor enforcement is identical to the regular request path; if today's at-work count has hit the floor, the report is denied rather than auto-approved. **Denied requests always carry a human-readable reason** in the `denial_reason` column; the reason is rendered under the Denied tag on the WFH list page, on the admin manage page, and in the email notification so a user never sees a bare "Denied" without context. Members can withdraw approved requests up until the WFH day itself. **The Request WFH form shows the quota for the period containing the picked date**, not always today's period — a member who's used 2/2 this month still sees 2 remaining when picking a date next month, and the submit button disables itself when the selected period is at the limit (with a help line under the date input explaining why; holidays disable the button the same way with a "this is a public holiday" notice). Admins can also "mark" a member as WFH for today from `Manage WFH &raquo; Mark member WFH` when the member worked from home but never submitted a request — the mark is a full override (bypasses the per-member quota and the on-site capacity floor) but is still counted in the math, so the dashboard reflects the corrected state. Admin-marked rows render in a distinct purple-blue chip color so the team can see at a glance which days were admin-asserted. A daily purge keeps the `wfh_requests` table bounded by hard-deleting rows older than the previous quota period (opt-out via `WFH_PURGE_ENABLED=false`). See the [Configuration](#work-from-home-wfh) table for the knobs.
- **Email notifications**: Team members are emailed on HAT-swap requests, WFH state changes, and cover assignments. See [NOTIFICATIONS.md](docs/NOTIFICATIONS.md).

## Quick Start

### Prerequisites
- Go 1.25 or later
- SQLite3 (included via github.com/ncruces/go-sqlite3)

### Installation
```bash
# Clone or download the project
cd madhatter

# Build the application
go build -o support-rota

# Run tests
go test ./...
```

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
./support-rota leave report alice@example.com sick 2024-01-15 2024-01-17

# Via Web Interface
# Navigate to /leave/report (requires login)
```

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
| `WFH_WITHDRAWAL_HOURS` | `24` | Hours before the WFH day after which an approved request can no longer be withdrawn by the member or an admin. |
| `WFH_REQUEST_HORIZON_DAYS` | `90` | Maximum number of days ahead a WFH request can be submitted. Requests beyond this horizon are rejected with a 422 in the API and a banner in the web form. |
| `WFH_PURGE_ENABLED` | `true` | When `true`, the daily scheduler hard-deletes `wfh_requests` rows whose date is strictly before the start of the previous quota period. The current and previous periods are always preserved. Opt out with `WFH_PURGE_ENABLED=false`. The same cutoff is exposed via `wfh purge [--apply]` and `/admin/wfh/purge`; both default to dry-run. |
| `WFH_SETTLEMENT_INTERVAL` | `15m` | Period between settlement scheduler ticks (Go duration format, e.g. `5m`, `1h`, `30s`). Lower values reduce the perceived latency between a request submission and the approve/deny decision; higher values save on CPU. |

#### Dashboard

| Variable | Default | Notes |
| --- | --- | --- |
| `HAT_LINK_URL` | none | URL the HAT day badge in the dashboard Today card links to (opens in a new window via `target="_blank" rel="noopener"`). When unset, the badge renders as a plain `<span>` (the original behavior). Useful for an on-call runbook, a Slack channel, or a PagerDuty rotation page. |

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

### Team Management
- `POST /api/v1/team` - Add team member
- `GET /api/v1/team` - List team members

### Leave Management
- `POST /api/v1/leave` - Report leave (triggers auto-cover)

### Schedule
- `POST /api/v1/schedule/generate` - Generate schedule for date range

### Calendar
- `POST /api/v1/calendar/subscribe` - Create calendar subscription
- `GET /api/v1/calendar/{token}/ics` - Get ICS calendar feed

### Holidays
- `GET /api/v1/holidays` - Get upcoming holidays
- `GET /api/v1/holidays/status` - Get holiday service status
- `POST /api/v1/holidays/refresh` - Manually refresh holidays

### OpenAPI Documentation
- `GET /docs` - Interactive OpenAPI documentation (auto-generated)

## Web Interface

### Pages
- `/` - Dashboard with current schedule and upcoming presence
- `/login` - Login page (redirects to OAuth provider)
- `/team` - Team management (admin only)
- `/leave/report` - Report leave (requires login)
- `/schedule/generate` - Schedule generation (admin only)
- `/calendar` - Calendar subscription management (requires login)

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
```

### Team Management
```bash
./support-rota team add "Name" email@example.com
./support-rota team list
```

### Leave Management
```bash
./support-rota leave report email@example.com sick 2024-01-15 2024-01-17
./support-rota leave list
```

### Schedule
```bash
./support-rota schedule generate 2024-01-01 2024-01-31
./support-rota schedule view 2024-01-15
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
./support-rota wfh purge --before 2024-01-01 --apply
```
The cutoff defaults to the start of the previous quota period (computed from `WFH_PERIOD_ANCHOR` and `WFH_PERIOD_DAYS`). The same operation is exposed at `GET /admin/wfh/purge` for a preview and `POST /admin/wfh/purge` to commit from the web UI. Errors with `WFH feature is disabled` when `WFH_ENABLED=false`.

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
- Tokens are not currently used for API access (future feature)

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

## Support

For issues or questions:
1. Check the logs for error messages
2. Verify configuration syntax
3. Test OAuth flow manually
4. Check database schema matches expected structure

## License

[Add license information here]

## Contributing

[Add contribution guidelines here]