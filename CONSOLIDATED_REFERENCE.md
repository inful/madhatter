# Support Rota System - Complete Reference

## 📋 Table of Contents

1. [Project Overview](#project-overview)
2. [System Architecture](#system-architecture)
3. [Technology Stack](#technology-stack)
4. [Database Schema](#database-schema)
5. [API Endpoints](#api-endpoints)
6. [CLI Commands](#cli-commands)
7. [Web Interface](#web-interface)
8. [Core Features](#core-features)
9. [Implementation Guide](#implementation-guide)
10. [Testing Strategy](#testing-strategy)
11. [Deployment](#deployment)
12. [Development Workflow](#development-workflow)

---

## Project Overview

The Support Rota System is a comprehensive solution for managing team support duties with:
- **Round-robin scheduling**: One weekday per person per rotation cycle
- **Leave management**: Unified system for sick leave, vacation, and other unavailability
- **Automatic cover assignment**: System automatically assigns covers when someone is on leave
- **Work From Home (WFH)**: Ad-hoc requests plus contractual recurring weekdays, with an auto-settling scheduler that respects a configurable on-site minimum and per-period quota. A daily purge hard-deletes rows from `wfh_requests` whose date is before the previous quota period (gated by `WFH_PURGE_ENABLED`, default `true`).
- **Calendar subscriptions**: Personal ICS calendar URLs for any calendar app
- **Web dashboard**: HTMX-based user interface
- **REST API**: HUMA-based API with automatic OpenAPI documentation
- **CLI tools**: Kong-based command-line interface
- **Defense in depth**: Per-IP rate limiting on auth and token endpoints, strict CSP + X-Frame-Options + HSTS-on-HTTPS response headers, AES-256-GCM-encrypted OAuth tokens at rest, SHA-256-hashed session cookies

---

## System Architecture

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   Web API       │    │  Core Services   │    │   CLI Tool      │
│   (HUMA)        │◄──►│   - Rota Engine  │◄──►│   (Kong)        │
│                 │    │   - Team Mgmt    │    │                 │
└─────────────────┘    │   - Leave Mgmt   │    └─────────────────┘
                       │   - Calendar     │
                       └──────────────────┘
                               ▲
                               │
                       ┌──────────────────┐
                       │  SQLite DB       │
                       └──────────────────┘
```

---

## Technology Stack

### Core Technologies
- **Language**: Go 1.25
- **API Framework**: HUMA v2 (OpenAPI-first REST API)
- **Web Framework**: HTMX (server-side rendering)
- **Database**: SQLite (file-based, zero setup)
- **CLI Framework**: Kong (type-safe commands)
- **Router**: Chi (lightweight HTTP router)

### Key Dependencies
```go
github.com/danielgtaylor/huma/v2
github.com/danielgtaylor/huma/v2/adapters/humachi
github.com/go-chi/chi/v5
github.com/alecthomas/kong
github.com/ncruces/go-sqlite3
github.com/google/uuid
```

### SQLC Configuration
**Database Layer**: Uses sqlc for type-safe SQL generation
- **Configuration**: `sqlc.yaml`
- **Generated Code**: `internal/database/sqlc/`
- **Benefits**: Compile-time SQL validation, type safety, reduced errors

**SQLC Commands**:
```bash
# Generate code
export PATH=$PATH:$(go env GOPATH)/bin && sqlc generate

# Run tests
go test ./internal/database -v
```

---

## Database Schema

The schema is the source of truth in `internal/database/sqlc/schema.sql`
(SQLC's single-file snapshot) and the `migrations/` directory (the
`golang-migrate` migrations applied on every `serve` startup). Both
files are co-maintained — adding a table or column requires a new
migration AND a regenerated `schema.sql`. For migration state in a
running deployment, run:

```bash
./support-rota migrate status
```

This document does not duplicate the schema; doing so would drift on
every migration (the codebase already has 29 migrations covering
team members, leave records, rota assignments, calendar subscriptions,
users, sessions, OAuth tokens, API tokens, hat swaps, notification
outbox and preferences, WFH requests and assignments, WFH co-presence
metrics, rotation state, and reassignment anchors — the full list
grows with every feature). For the current state, read the two files
above directly.

`leave_records.leave_type` is the one schema field worth calling out
explicitly: it tags a leave as plain `leave` (default) or `conference`.
The tag is purely a UI signal — the dashboard's "Today" badge swaps
"On leave" for "@conference" and the schedule-matrix cell swaps the
plane icon for a people-group icon. Scheduling, cover assignment,
and quotas are unchanged.

---

## API Endpoints

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

### WFH Management
- `POST /api/v1/wfh` - Submit a WFH request (ad_hoc). Date in YYYY-MM-DD.
- `POST /api/v1/wfh/report-today` - Self-service "WFH today" for unforeseen circumstances.
- `GET /api/v1/wfh` - List WFH requests. Regular users see their own; admins see all. Each row exposes an `origin` field with one of `ad_hoc`, `recurring`, `assigned`, `swap` so API consumers can distinguish self-requested days from system-assigned or peer-swapped rows.
- `POST /api/v1/wfh/{id}/cancel` - Cancel an own pending WFH request.
- `POST /api/v1/wfh/{id}/withdraw` - Admin withdraws an approved WFH.
- `POST /api/v1/wfh/settle` - Force the settlement scheduler to run.
- `GET /api/v1/wfh/quota` - Per-member quota status (used/remaining/period).
- `GET /api/v1/wfh/date/{date}` - Admin: list all WFH rows for a date.
- `POST /api/v1/wfh/swap/{swapId}/accept` - Target accepts a WFH swap.
- `POST /api/v1/wfh/swap/{swapId}/reject` - Target rejects a WFH swap.
- `POST /api/v1/wfh/swap/{swapId}/cancel` - Requester cancels a pending WFH swap.

### OpenAPI Documentation
- `GET /docs` - Interactive OpenAPI documentation (auto-generated by HUMA)

---

## CLI Commands

### Server
```bash
./support-rota serve --port 8080
```

### Team Management
```bash
# Add team member
./support-rota team add "Alice Johnson" alice@example.com

# List team members
./support-rota team list
```

### Leave Management
```bash
# Report leave (always records LeaveTypeLeave; for conference leave
# use the web form at /leave/report).
./support-rota leave report alice@example.com <YYYY-MM-DD> <YYYY-MM-DD>

# List all leave records
./support-rota leave list
```

### Schedule
```bash
# Generate schedule
./support-rota schedule generate 2024-01-01 2024-01-31

# View schedule for specific date
./support-rota schedule view 2024-01-15
```

### Calendar
```bash
# Subscribe to calendar
./support-rota calendar subscribe alice@example.com

# Export ICS file
./support-rota calendar export alice@example.com alice.ics
```

### WFH Management
```bash
# Dry-run: print the cutoff date and the number of rows that would be deleted.
./support-rota wfh purge

# Commit the deletion.
./support-rota wfh purge --apply

# Override the cutoff for a one-off catch-up clean (YYYY-MM-DD).
./support-rota wfh purge --before 2024-01-01 --apply
```
The cutoff defaults to the start of the previous quota period (computed from `WFH_PERIOD_ANCHOR` and `WFH_PERIOD_DAYS`). Errors with `WFH feature is disabled` when `WFH_ENABLED=false`. The same operation is exposed at `GET /admin/wfh/purge` (preview) and `POST /admin/wfh/purge` (commit).

### Migration Management
```bash
# Print the database's migration state without applying pending migrations.
./support-rota migrate status
```
Reads `support_rota.db` via `database/sql` directly (not `database.New`) so a dirty or pending schema never blocks the inspection. Reports the applied version, dirty flag, latest migration on disk, and the count of pending migrations. Migrations still auto-apply on every `serve` startup — this command is for diagnostics. When the database is dirty, the output includes the SQL to clear the flag.

---

## Web Interface

### Pages
- **Dashboard** (`/`) - Current schedule and quick actions
- **Team Management** (`/team`) - Add/list team members
- **Leave Report** (`/leave/report`) - Report your own leave (or any member's, admin only) — arbitrary date range
- **Someone Called In Sick** (`/leave/report-sick`) - Same-day leave on behalf of another team member (any authenticated user). Pinned to today; date inputs are server-validated; duplicates refused. The non-admin relax of the regular leave-report path so a colleague's call-in can be registered without admin intervention.
- **Schedule View** (`/schedule/current`) - Current week schedule
- **Calendar** (`/calendar`) - Calendar subscription management
- **WFH Manage** (`/admin/wfh`) - Approve, deny, and withdraw WFH requests (admin only)
- **WFH Purge** (`/admin/wfh/purge`) - Preview and hard-delete `wfh_requests` rows older than the previous quota period (admin only)

### HTMX Features
- Dynamic form submissions without page reloads
- Real-time schedule updates
- Progressive enhancement (works without JS)

---

## Core Features

### 1. Round-Robin Scheduling
- Each team member gets exactly one weekday per rotation cycle
- Weekdays only (Monday-Friday)
- Fair distribution algorithm

### 2. Leave Management
- **Unified system**: Single "leave" concept for sick, vacation, and other
- **Automatic cover assignment**: System immediately assigns covers
- **Fair selection**: Chooses next available person in rotation

### 3. Automatic Schedule Maintenance
- **14-day rolling schedule**: Automatically maintained at all times
- **Static as possible**: Preserves existing assignments, only fills gaps
- **Event-driven**: Triggers on team changes, leave reports, and web requests
- **Smart regeneration**: Supports both "fill gaps" and "regenerate from scratch" modes
- **No manual intervention**: Schedule stays current without user action

**Key Methods**:
- `EnsureSchedule()` - Creates 14-day rolling schedule automatically
- `GenerateMissingDays()` - Fills gaps while preserving existing assignments
- `RegenerateSchedule()` - Deletes and recreates schedule from scratch
- `HandleTeamChange()` - Updates schedule when team changes
- `HandleLeaveChange()` - Creates cover assignments when members take leave

### 4. Calendar Subscriptions
- **Unique URLs**: Personal calendar URL for each team member
- **ICS format**: Compatible with Google Calendar, Outlook, Apple Calendar
- **Real-time updates**: Changes reflect immediately
- **Copy Button**: One-click URL copying with visual feedback notification
- **User Experience**: No manual text selection needed

### 4. Fairness Algorithm
```
Example: Team [Alice, Bob, Charlie]
Week 1: Alice, Bob, Charlie, Alice, Bob
Week 2: Charlie, Alice, Bob, Charlie, Alice

If Alice is on leave Mon-Tue of Week 2:
- Mon: Bob covers (next in rotation)
- Tue: Charlie covers (next in rotation)
- Wed: Alice's slot (she's back)
- Thu: Bob (continues rotation)
- Fri: Charlie (continues rotation)

Result: Bob and Charlie get extra days, Alice gets fewer this cycle
```

---

## Implementation Guide

### Step 1: Project Setup
```bash
mkdir support-rota
cd support-rota
go mod init github.com/madhatter/support-rota

# Install dependencies
go get github.com/danielgtaylor/huma/v2
go get github.com/danielgtaylor/huma/v2/adapters/humachi
go get github.com/go-chi/chi/v5
go get github.com/alecthomas/kong
go get github.com/ncruces/go-sqlite3
go get github.com/google/uuid
```

### Step 2: Directory Structure
```
support-rota/
├── cmd/
│   └── root.go              # CLI entry point
├── internal/
│   ├── api/
│   │   ├── server.go        # HUMA server
│   │   └── handlers.go      # API handlers
│   ├── database/
│   │   ├── db.go            # SQLite connection
│   │   ├── models.go        # Data models
│   │   ├── leave.go         # Leave operations
│   │   └── rota.go          # Rota operations
│   ├── rota/
│   │   └── engine.go        # Scheduling engine
│   ├── calendar/
│   │   └── ics.go           # ICS generation
│   └── web/
│       ├── handlers.go      # Web UI handlers
│       └── templates/       # HTML templates
├── main.go                  # Entry point
└── go.mod
```

### Step 3: Database Layer
Create `internal/database/db.go` with SQLite connection and schema setup.

**SQLC Migration**: The database layer now uses sqlc for type-safe SQL generation:
- `sqlc.yaml` - Configuration file
- `internal/database/sqlc/` - Generated type-safe code
- `internal/database/db.go` - Updated to use sqlc
- `internal/database/leave.go` - Leave operations with sqlc
- `internal/database/rota.go` - Rota operations with sqlc

**Benefits**:
- Compile-time SQL validation
- Type-safe query methods
- Reduced boilerplate code
- Better IDE support

### Step 4: Core Logic
Create `internal/rota/engine.go` with round-robin scheduling and cover assignment.

### Step 4a: Automatic Schedule Maintenance
Create `internal/rota/maintenance.go` with the `ScheduleMaintenance` service:

```go
type ScheduleMaintenance struct {
    db     *database.DB
    engine *Engine
}

func (sm *ScheduleMaintenance) EnsureSchedule() error {
    // Automatically maintains 14-day rolling schedule
    // Called by web handlers on key events
}

func (sm *ScheduleMaintenance) GenerateMissingDays(startDate, endDate time.Time) (bool, error) {
    // Fills gaps while preserving existing assignments
    // Returns true if assignments were created
}
```

**Integration Points**:
- Dashboard handler: Calls `EnsureSchedule()` on every page load
- Team handler: Calls `HandleTeamChange()` when members added/removed
- Leave handler: Calls `HandleLeaveChange()` when leave reported
- Schedule generate: Supports both "fill gaps" and "regenerate" modes

**Key Benefits**:
- No manual schedule generation needed
- Preserves existing assignments ("static as possible")
- Automatically adapts to team changes
- Creates cover assignments on leave

### Step 5: HUMA API
Create `internal/api/server.go` with HUMA setup and OpenAPI generation.

### Step 6: Web Interface
Create `internal/web/handlers.go` and HTML templates with HTMX.

### Step 7: CLI
Create `cmd/root.go` with Kong command structure.

---

## Testing Strategy

### Unit Tests
- **Database Layer**: 95%+ coverage
- **Rota Engine**: 100% coverage
- **API Handlers**: 90%+ coverage
- **Web Handlers**: 85%+ coverage
- **CLI**: 80%+ coverage

### Test Structure
```go
func TestFeature(t *testing.T) {
    // Arrange
    db, cleanup := setupTestDB(t)
    defer cleanup()
    
    // Act
    result, err := db.SomeOperation()
    
    // Assert
    require.NoError(t, err)
    assert.Equal(t, expected, result)
}
```

### Running Tests
```bash
# All tests
go test ./... -v -cover

# Specific package
go test ./internal/database -v

# With race detector
go test -race ./...
```

---

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

---

## Development Workflow

### Daily Workflow
1. **Morning**: Pick next test to write
2. **Write test** (RED) - commit as `test(scope): add test for feature`
3. **Write code** (GREEN) - commit as `feat(scope): implement feature`
4. **Refactor** - commit as `refactor(scope): improve code`
5. **Afternoon**: Review, plan next day's tests

### Pre-Commit Checklist
```bash
# 1. Run all tests
go test ./... -v -cover

# 2. Run linter
golangci-lint run

# 3. Build
go build -o support-rota

# 4. Run race detector
go test -race ./... -short
```

### Commit Message Format
```
type(scope): description

Examples:
- feat(database): add team member CRUD operations
- fix(api): correct leave response format
- test(rota): add cover assignment tests
- refactor(web): optimize HTMX templates
- docs: add API documentation
- chore: update dependencies
```

---

## See also

- **README.md** — Primary user + operator documentation, env-var reference, and quick start.
- **AGENTS.md** — Developer guidance, security guarantees, and documentation triggers.
- **AUTH_SETUP.md** — OAuth2 setup for Forgejo and GitLab.
- **API_AUTH_IMPLEMENTATION.md** — API token design and security model.
- **HOLIDAY_IMPLEMENTATION.md** — Holiday subsystem architecture (note: some env vars in this doc are not read by the code; see [`README.md: Holidays and database`](../README.md#holidays-and-database)).
- **SQLC_MIGRATION_GUIDE.md** — Historical account of the SQLC migration (see [`internal/database/sqlc/queries/USAGE.md`](../internal/database/sqlc/queries/USAGE.md) for the current caller-pattern note).
- **docs/NOTIFICATIONS.md** — Notification pipeline, unsubscribe flow, ops queries.
- **docs/ASSIGNED_WFH.md** — Seat-cap feature reference.
- **plans/assigned-wfh-plan.md** — Implementation plan with shipped-drift tracker (historical planning artifact).
- **`internal/api/operations.go`** — Canonical source for the API surface; published as the live OpenAPI document at `GET /docs`.
- **`internal/web/routes.go`** — Canonical source for the web route table.