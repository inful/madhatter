# AGENTS.md

This file provides guidance to agents when working with code in this repository.

## Project Overview

**Support Rota System** - A comprehensive support duty management system with automatic scheduling, leave management, calendar subscriptions, and OAuth2 authentication.

## Technology Stack

- **Language**: Go 1.25
- **API Framework**: HUMA v2 with go-chi/chi router
- **Web Framework**: HTMX (server-side rendering)
- **Database**: SQLite (github.com/ncruces/go-sqlite3)
- **CLI Framework**: Kong
- **Authentication**: OAuth2 (Forgejo, GitLab)
- **Calendar Format**: ICS (iCalendar)
- **SQL Generation**: sqlc for type-safe SQL
- **Linter**: golangci-lint version v2.8.0

## Non-Obvious Project-Specific Information

### Agent instructions

- When working with software libraries, API, third party tools, etc, first check with the context7 mcp for the most up to date documentations.
- For anything that involves complex analysis, planning and designing, use sequential thinking mcp.
- Always use conventional commits; amend if necessary to keep history clean.
- Stage only relevant files for each commit (avoid `git add -A`).
- Fix all `golangci-lint` issues before committing.
- Run the full test suite (`go test ./...`) and ensure it passes before committing.

### Database
- Uses `github.com/ncruces/go-sqlite3` (`sqlite3` driver with embedded SQLite)
- Database file: `support_rota.db`
- **Critical**: Foreign keys must be enabled manually with `PRAGMA foreign_keys = ON`
- All dates stored as DATE strings in format `"2006-01-02"`
- SQLC generates `time.Time` for dates, but existing code uses string dates - use wrapper for compatibility

### Database Migrations
- Uses `golang-migrate/migrate` for schema versioning
- Migration files located in `migrations/` directory at project root
- Migration file naming: `NNNNNN_description.up.sql` and `NNNNNN_description.down.sql`
- Migrations run automatically on database initialization via `database.RunMigrations()`
- Migration path resolution uses multiple fallback methods:
  1. `MIGRATIONS_PATH` environment variable
  2. Search up to 3 directories from working directory
  3. Relative to source file location (internal/database -> root)
- Compatible with `github.com/golang-migrate/migrate/v4/database/sqlite` via the `sqlite3` database/sql driver

**Migration Commands:**
```bash
# Migrations run automatically during database.New()
# Manual migration status check
go run main.go migrate-status

# Create new migration
# File format: migrations/NNNNNN_description.{up,down}.sql
```

**Key Migration Files:**
- `migrations/` - All migration files
- `internal/database/migrate.go` - Migration runner and utilities
- `internal/database/db.go` - Calls RunMigrations() on init

**Migration Functions:**
- `RunMigrations(db)` - Apply all pending migrations
- `GetMigrationVersion(db)` - Get current migration version
- `GetMigrationStatus(db)` - Detailed migration status
- `RollbackMigration(db)` - Rollback last migration (use with caution)
- `MigrateToVersion(db, version)` - Migrate to specific version

**Schema Change Workflow:**
1. Create migration files (NNNNNN_description.{up,down}.sql)
2. Update `internal/database/sqlc/schema.sql` to match final state
3. Run `sqlc generate` to update generated code
4. Test migration on existing database
5. Add integration tests to verify schema compatibility
6. Commit migration files, schema.sql, and generated code together

**Important:** Never modify schema.sql without a corresponding migration. Migrations are the source of truth for schema changes.

### CLI Framework
- Uses `github.com/alecthomas/kong` for CLI parsing
- Commands are defined in a struct hierarchy in `cmd/root.go`
- Parsed with `kong.Parse(&CLI)`

### API Framework
- Uses `github.com/danielgtaylor/huma/v2` with `go-chi/chi` adapter
- All API operations must be registered via `huma.Register()`
- OpenAPI documentation auto-generated at `/docs`

### Date Handling
- All dates use Go's reference format `"2006-01-02"` for parsing and formatting
- Database stores dates as DATE strings
- Weekend skipping: `date.Weekday() == time.Saturday || date.Weekday() == time.Sunday`

### Round-Robin Scheduling
- Engine uses round-robin assignment with weekend skipping
- Covers reference original assignments via `original_assignment_id` foreign key
- **Independent R2 cover rotation**: Cover assignments use a separate rotation from original assignments
- **Static scheduling**: `GenerateMissingDays()` preserves existing assignments and only fills gaps

### Automatic Schedule Maintenance
The system automatically maintains a 14-day rolling schedule using `ScheduleMaintenance` service in `internal/rota/maintenance.go`:

- `EnsureSchedule()` - Creates 14-day rolling schedule automatically
- `GenerateMissingDays()` - Fills gaps while preserving existing assignments ("static as possible" scheduling)
- `RegenerateSchedule()` - Deletes and recreates schedule from scratch
- `HandleTeamChange()` - Updates schedule when team members change
- `HandleLeaveChange()` - Creates cover assignments when members take leave
- Web handlers automatically trigger schedule maintenance on key events

### WFH Recurring Materialization
Recurring-WFH is realized as ordinary approved `wfh_requests` rows with
`is_recurring=1`. The materializer (`internal/wfh/recurring_materializer.go`)
walks a date range, finds each active member's contractual weekdays
(`team_members.recurring_wfh_{mon..fri}`), and inserts any missing rows as
auto-approved. It runs from:
- `SettlePendingRequests` (covers the next `SettlementDays`).
- `handleWFHList` on each page load (covers the current period).
- `EnsureRecurringMaterializedForMember` for a single member.

Idempotent: the `UNIQUE(member_id, date)` constraint and the pre-insert
existence check block duplicates. A user-withdrawn recurring row blocks
re-materialization (the user's intent is preserved).

### WFH Past-Period Purge
The `wfh_requests` table accumulates history forever by default. To keep
the table bounded, the WFH service hard-deletes rows whose `date` is
strictly before the start of the previous quota period. The cutoff moves
forward with the calendar so the same `date < cutoff` SQL works on every
run. Three surfaces invoke the same purge:
- **Scheduler**: `Scheduler.runSettle` calls `Service.PurgePastPeriods`
  after each settlement tick. Gated by `WFH_PURGE_ENABLED` (default `true`)
  AND `WFH_ENABLED` — disabling the feature turns the purge off everywhere.
- **CLI**: `wfh purge [--apply] [--before YYYY-MM-DD]`. Dry-run by default;
  `--before` overrides the period-derived cutoff for one-off cleans. Errors
  with `WFH feature is disabled` when the service is off.
- **Admin web**: `GET /admin/wfh/purge` shows the cutoff + would-delete
  count; `POST` with `confirm=true` commits and redirects to
  `/admin/wfh?wfh_purged=N&cutoff=YYYY-MM-DD` with a flash banner.

`Service.PurgePastPeriods` and `Service.PurgePastPeriodsDryRun` both
short-circuit to `(0, nil)` when `IsPurgeEnabled()` is false. The dry-run
preview uses `Database.CountWFHRequestsBefore` (a `SELECT COUNT(*)`)
so admin GETs never mutate the table. Errors are logged at the scheduler
boundary and never block settlement; the next tick retries.

### Calendar ICS Generation
- ICS files are generated with 0o600 permissions
- Calendar subscriptions use UUID tokens stored in `calendar_subscriptions` table
- Uses `github.com/arran4/golang-ical` library
- **Calendar event descriptions are templated**. Meetings, support assignments, leave, and holidays each have a `text/template` and an `html/template` override wired through environment variables. Built-in defaults reproduce the previous hard-coded output exactly. Support, leave, and holiday templates share a per-day **presence snapshot** (active count, on-site/on-leave/WFH lists, HAT name, stable random order) computed fresh per request from the database. See the "Calendar template overrides" section of the README for the data model and examples.

### Notifications
- `internal/notify` is the public API producer code uses (`notify.Notifier`). Methods never return an error and never block on network I/O — handlers call them and return.
- Production notifier writes to the `notification_outbox` table (migration 000013). A worker goroutine drains the table, dispatches to the registered channel, and reschedules failures with exponential backoff capped at 1h.
- Today the only delivery channel is **email**, built on `github.com/jordan-wright/email` + `net/smtp` (the `nikoksr/notify/service/mail` shim was dropped because it doesn't expose MIME headers for `List-Unsubscribe`). The architecture is designed for multiple channels: `internal/notify/channels/` is the registry; adding Slack or Teams is a self-contained addition with no producer-code changes.
- Templates are `text/template` (plain text, no multipart) bundled via `//go:embed` and overridable per-event via `NOTIFY_<EVENT>_TXT_PATH` / `_SUBJECT_TXT_PATH` env vars, mirroring the calendar event-template pattern.
- One-click unsubscribe: every email body gets a per-recipient footer link and the email carries `List-Unsubscribe` / `List-Unsubscribe-Post` headers (RFC 8058). Tokens are HMAC-signed with `SESSION_SECRET`; the public one-click endpoint is `GET /unsubscribe?token=…` and the resume endpoint is `POST /unsubscribe/resume`. Per-member state lives in `notification_preferences` (migration 000014); the absence of a row is "default enabled". See `docs/NOTIFICATIONS.md` for the full reference: what fires when, env vars, ops queries, the "add a new channel" checklist.

### Test Structure
- Tests are co-located with source files (e.g., `db_test.go` next to `db.go`)
- All tests use testify assertions
- Testifylint compliant
- **Use testing package helpers**: Always use `t.Setenv()` instead of `os.Setenv()` and `t.Chdir()` instead of `os.Chdir()` in tests
  - These helpers automatically clean up after tests
  - No need for manual defer cleanup functions
  - Prevents environment pollution between tests
  - Enforced by golangci-lint's `usetesting` rule

### Time-Bound Tests (Go 1.25+)
- For any test that waits for time to pass — `time.Sleep`, polling `require.Eventually`, scheduler/ticker tests, goroutine-synchronization tests — use `testing/synctest` instead of real time.
- Real-time tests are slow or flaky. `synctest` provides fake time and `synctest.Wait()` for deterministic quiescence.
- Workflow: wrap the test body in `synctest.Test(t, func(t *testing.T) { ... })`, then replace `time.Sleep` / `Eventually` with `synctest.Wait()` after the operation that triggers async work.
- **I/O is not durably blocking inside a bubble.** For tests that need network I/O, use `httptest.NewServer` (real I/O, run outside the bubble) or inject a stub via a small interface (run inside the bubble).
- **Mutexes are not durably blocking** — do not write tests that block only on a mutex; ensure time/channel/waitgroup is also involved.
- See `.claude/skills/testing-synctest/SKILL.md` for the full pattern reference, including the durable-block table and migration checklist.

### SQLC Migration
**Key files:**
- `sqlc.yaml` - sqlc configuration
- `internal/database/sqlc/` - Generated type-safe code
- `internal/database/sqlc_wrapper.go` - Backward compatibility wrapper
- `SQLC_MIGRATION_GUIDE.md` - Detailed migration documentation

**SQLC Commands:**
```bash
export PATH=$PATH:$(go env GOPATH)/bin && sqlc generate
go test ./internal/database -v
```

**Database Methods** (new for schedule maintenance):
- `GetAssignmentsByDateRange()` - Get assignments within date range
- `GetLatestAssignmentDate()` - Find most recent assignment date
- `DeleteAssignmentsInRange()` - Delete assignments in range for regeneration

### Critical Gotchas

1. **Foreign Keys**: Must be enabled manually with `PRAGMA foreign_keys = ON`
2. **Leave Assignment**: Creates cover records that reference original assignments
3. **Weekend Skipping**: Automatically skipped in scheduling
4. **Calendar Subscriptions**: Require creating a token entry before generating ICS feeds
5. **SQLC Date Handling**: Generates `time.Time` for dates, but existing code uses string dates
6. **SQLC Null Handling**: Uses `sql.NullInt64` for nullable integers - wrapper converts to/from bool
7. **Static Scheduling**: `GenerateMissingDays()` preserves existing assignments and only fills gaps
8. **Template Compatibility**: Dashboard template handles string dates directly (no `.Format` calls)
9. **No Duplicate Days**: Only one assignment per day (original or cover, not both)
10. **Migration Workflow**: Schema changes must be done via migrations - never directly edit db.go
11. **Migration Testing**: Always test both up and down migrations before committing
12. **Migration Path**: Migrations are found via env var, working dir search, or source file location
13. **Schema Synchronization**: Always create migration when updating schema.sql - migrations are source of truth
14. **Integration Testing**: Add integration tests for any new database operations to catch schema issues early

### Authentication System

**OAuth2 Providers**: Forgejo and GitLab (extensible to others)

**Database Tables**:
- `users` - User accounts with OAuth2 metadata
- `sessions` - Active user sessions (tokens are hashed using SHA-256)
- `oauth_tokens` - Stored OAuth2 tokens (encrypted at rest using AES-256-GCM)

**Session Management**:
- Tokens are hashed with SHA-256 before storage
- Sessions expire after 24 hours by default
- Secure cookies with HttpOnly and SameSite flags

**User Roles**:
- **Admin**: First user to login automatically becomes admin
- **Regular**: Subsequent users

**Middleware**:
- `RequireAuth` - Requires authentication
- `RequireAdmin` - Requires admin privileges

**Configuration**: YAML-based OAuth provider settings

**Admin Features**: Team management, schedule generation, leave approval

**User Features**: View schedules, report leave, calendar subscriptions

**Auth Flow**:
1. User clicks login → Redirects to provider
2. Provider callback → Exchange code for token
3. Get user email → Check/create user account
4. Create session → Set secure cookie
5. Middleware validates session on protected routes

**Critical Auth Gotchas**:
- First user to login becomes admin automatically
- Sessions expire after 24 hours by default
- OAuth tokens are stored but not currently used (for future API access)
- Provider base URLs must be accessible from the server
- Callback URLs must be registered exactly with providers
- Session secret must be strong and kept secure
- Database foreign keys must be enabled for session cleanup
- Admin privileges required for team/leave/schedule operations

### Development Mode

The system supports a development mode with fake OAuth authentication:

- **CLI Flag**: `--development` or `--development=true`
- **Command**: `./support-rota serve --port 8080 --development`
- **Features**: Bypasses full OAuth setup, uses fake provider
- **Web Interface**: Special development login page at `/login`
- **Auto-login**: Creates admin user automatically

This is implemented in:
- `internal/auth/fake_provider.go`
- `internal/auth/fake_provider_test.go`
- `internal/api/server.go` (setupDevelopmentAuth function)

### Web Interface Features

**Calendar Subscriptions**:
- Copy button with clipboard API
- Visual notification system
- Responsive flexbox layout
- One-click URL copying

**Schedule Display**:
- Shows cover badges and leave indicators
- Automatic maintenance triggers
- Presence tracking for next business days

**Automatic Maintenance**:
- Triggers on team changes
- Triggers on leave reports
- Triggers on page loads
- 14-day rolling schedule

**Dual-Mode Schedule Generation**:
- **Fill Gaps** (default): Preserves existing assignments, only fills missing days
- **Regenerate**: Deletes and recreates schedule from scratch

### Holiday Support

**Implementation**: `internal/holiday/` package

**Features**:
- Automatic fetching from iCal URLs
- Background scheduler (daily updates)
- Integration with schedule engine
- API endpoints for status and refresh

**Environment Variables**:
- `HOLIDAY_URLS` - Comma-separated iCal URLs
- `HOLIDAY_FETCH_INTERVAL` - Hours between fetches (default: 24)
- `HOLIDAY_LOOKAHEAD` - Days to look ahead (default: 365)

**Components**:
- `service.go` - Main service coordinating store and scheduler
- `scheduler.go` - Background job for fetching holidays
- `ical.go` - iCal parsing and fetching
- `store.go` - In-memory holiday storage

## Code Quality Standards

### Linter Configuration
- **Tool**: golangci-lint v2
- **Issues Allowed**: 0
- **Always run with**: `--fix` flag to auto-fix formatting

### Cyclomatic Complexity
- **Limit**: 10 per function
- **Enforcement**: golangci-lint with cyclop rule

### Formatting
- **Tool**: gofumpt
- **Compliance**: Required

### Comments
- **Style**: All comments must end with periods (godot rule)
- **Enforcement**: golangci-lint with godot rule

### Test Assertions
- **Library**: testify
- **Compliance**: testifylint compliant
- **Style**: Use `require.NoError(t, err)` for errors, `assert.Equal(t, expected, actual)` for values

### Testing Strategy
- **Integration Tests**: Must test complete user flows through web handlers
- **Database Tests**: Must verify schema compatibility with current migrations
- **Schema Validation**: Integration tests should catch schema mismatches
- **Example**: `internal/web/handlers_leave_test.go` demonstrates proper integration testing
- **Key principle**: Tests should catch issues that users would encounter

### Web Templates
- **Requirements**: 
  - Include copy functionality for URLs
  - Visual feedback notifications
  - Responsive design
  - HTMX integration

### Code Organization
- **Tests**: Co-located with source files (e.g., `db_test.go` next to `db.go`)
- **Integration Tests**: Separate files with `_integration_test.go` or feature-specific naming
- **Package Structure**: Internal packages for domain logic
- **API Registration**: All endpoints registered in `internal/api/server.go`

## Common Commands

### Build and Test
```bash
# Build
go build -o support-rota

# Run all tests
go test ./... -v -cover

# Run specific package tests
go test ./internal/database -v

# Race detector
go test -race ./...
```

### Code Quality
```bash
# Lint check
golangci-lint run

# Auto-fix issues
golangci-lint run --fix

# Format code
go fmt ./...
gofumpt -w .
```

### SQLC
```bash
# Generate code
export PATH=$PATH:$(go env GOPATH)/bin
sqlc generate

# Run database tests
go test ./internal/database -v
```

### Development
```bash
# Development mode (no OAuth required)
./support-rota serve --port 8080 --development

# Production mode
./support-rota serve --port 8080
```

### Migrations
```bash
# Migrations run automatically during database initialization
# To check migration status programmatically, use the functions in internal/database/migrate.go

# Create new migration
# 1. Create two files in migrations/:
#    - NNNNNN_description.up.sql (apply changes)
#    - NNNNNN_description.down.sql (rollback changes)
# 2. Use incrementing numbers (e.g., 000001, 000002, etc.)
# 3. Test both up and down migrations
# 4. Update schema.sql to reflect final state (for SQLC)
# 5. Run `sqlc generate` to update generated code

# Example:
# migrations/000002_add_user_preferences.up.sql
# migrations/000002_add_user_preferences.down.sql
```

## File Structure Key

```
madhatter/
├── cmd/
│   └── root.go                    # CLI command definitions
├── internal/
│   ├── api/
│   │   └── server.go              # HUMA API server
│   ├── auth/
│   │   ├── config.go              # OAuth configuration
│   │   ├── session.go             # Session management
│   │   ├── middleware.go          # Auth middleware
│   │   ├── fake_provider.go       # Development mode provider
│   │   └── handlers.go            # Auth handlers
│   ├── database/
│   │   ├── db.go                  # Database wrapper
│   │   ├── migrate.go             # Migration runner and utilities
│   │   ├── models.go              # Go models
│   │   ├── sqlc/                  # Generated SQLC code
│   │   │   ├── schema.sql         # Database schema
│   │   │   ├── queries/           # SQL queries
│   │   │   └── *.sql.go           # Generated Go code
│   │   └── sqlc_wrapper.go        # Backward compatibility
│   ├── rota/
│   │   ├── engine.go              # Scheduling engine
│   │   ├── maintenance.go         # Automatic maintenance
│   │   └── *_test.go              # Tests
│   ├── holiday/
│   │   ├── service.go             # Main service
│   │   ├── scheduler.go           # Background fetcher
│   │   ├── scheduler_test.go      # Scheduler tests
│   │   ├── ical.go                # iCal parsing
│   │   └── store.go               # In-memory storage
│   ├── calendar/
│   │   ├── ical.go                # ICS generation
│   │   └── ical_test.go           # Tests
│   ├── wfh/
│   │   ├── service.go             # WFH request settlement service
│   │   ├── scheduler.go           # Settlement scheduler
│   │   ├── service_test.go        # Service tests
│   │   └── scheduler_test.go      # Scheduler tests (uses synctest)
│   └── web/
│       ├── handlers.go            # Web UI handlers
│       └── templates/             # HTML templates
├── migrations/                     # Database migrations
│   ├── 000001_initial_schema.up.sql
│   ├── 000001_initial_schema.down.sql
│   └── ...
├── plans/                          # Documentation plans
├── sqlc.yaml                       # SQLC configuration
├── go.mod                          # Go dependencies
└── main.go                         # Entry point
```

## Pre-Commit Checklist

Before committing changes, ensure:

1. **All tests pass**: `go test ./... -v`
2. **No linter issues**: `golangci-lint run`
3. **Code formatted**: `gofumpt -w .`
4. **SQLC generated** (if schema/queries changed): `sqlc generate`
5. **Documentation triggers checked**: see [Documentation Triggers](#documentation-triggers) below. For every changed file, identify which docs need updating and include them in the same commit. Stale docs caught later in a release tag are a smell — every change ships with its own docs.
6. **No broken links**: Verify all file references in docs

## Documentation Triggers

Documentation is part of the same commit as the change that requires it — not a follow-up. When the commit lands, the docs in `main` already describe the new behaviour. For every change, walk this table before committing:

| Change category | Files to update | Specific edits |
|---|---|---|
| New or renamed env var in `internal/*/service.go` `LoadConfigFromEnv` | `README.md` config table (env-vars section) and `internal/web/templates/help.html` (config table if the var surfaces in `/help`) | Add a row to the env-var table with default + meaning; if it appears in `help_handler.go`, also add a row to the help config table |
| Existing env var's default changes | `README.md` config table, `help.html` config table, and any in-line doc comment that quotes the old default | Update the default column; grep for the var name in `*.md` and `*.html` to catch inline references |
| New user-facing web feature | `README.md` (Features list) and `internal/web/templates/help.html` (relevant section) | Add a bullet to the matching feature section; add a paragraph to help if the feature has user-visible behaviour worth explaining |
| New user-facing API endpoint | `README.md` (Features) and `CONSOLIDATED_REFERENCE.md` (API Endpoints) | Bullet + endpoint entry |
| New CLI subcommand | `README.md` (Features / CLI) and `CONSOLIDATED_REFERENCE.md` (CLI Commands) | Add to both lists |
| In-app help text | `internal/web/templates/help.html` | Update the prose and config table; check the handler that sets the data context (`help_handler.go`) exposes any new fields |
| Internal refactor (no user-visible change) | Nothing | Don't add user-facing noise — but do mention internal-only changes in the commit body so the next agent knows the surface didn't change |
| Bug fix in user-visible flow | Mention in commit body | The PR description is the changelog; no separate doc file unless the bug had a documented workaround |

**Concrete commands to run before commit** when the change touches an env var, the help page, or the README feature list:

```sh
# 1. If you added or renamed an env var, confirm the row exists in both:
grep -n "WFH_.*=" README.md        # adjust prefix; covers all WFH_ env vars
grep -n "WFH_.*=" internal/web/templates/help.html
# If grep misses a var, you forgot to document it.

# 2. If you changed an env-var default, the README and help.html
#    tables must show the new value, not the old one. Grep for
#    the var name and read the line.
grep -A1 "WFH_SETTLEMENT_DAYS" README.md
grep -A1 "WFH_SETTLEMENT_DAYS" internal/web/templates/help.html

# 3. If you added a help-handler field, the template must reference it.
grep -n "WFH" internal/web/templates/help.html | grep -v "<!--"
```

A PR is incomplete if a code change ships without its documentation. The CI / lint gate does not catch this — only the author can.

## Common Pitfalls to Avoid

1. **Forgetting foreign keys**: Always enable with `PRAGMA foreign_keys = ON`
2. **Date format mismatches**: Use `"2006-01-02"` consistently
3. **SQLC null handling**: Use wrapper for bool/int conversions
4. **Duplicate assignments**: Check for existing before creating
5. **Session security**: Always hash tokens before storage
6. **OAuth callback URLs**: Must match exactly with provider
7. **Development mode**: Use `--development` flag for local testing
8. **Holiday checking**: Use engine's holiday checker, not just weekends
9. **Cover rotation**: R2 is independent from R1 original rotation
10. **Static scheduling**: Preserve existing assignments when filling gaps

## Testing Guidelines

### Unit Tests
- Co-located with source files
- Use testify assertions
- Test all error paths
- Mock external dependencies

### Integration Tests
- Test database operations
- Test API endpoints
- Test authentication flows
- Test schedule generation

### Test Data
- Use test database or in-memory SQLite
- Clean up after tests
- Use realistic scenarios

## Deployment Considerations

### Environment Variables
- `SESSION_SECRET` - Required for production
- `TOKEN_ENCRYPTION_KEY` - Required for OAuth token encryption
- OAuth provider credentials - Required for production
- `HOLIDAY_URLS` - Optional for holiday support

### Security
- Use HTTPS in production
- Set strong session secrets
- Enable token encryption
- Restrict database file permissions
- Regular backups

### Performance
- SQLite is file-based - consider connection pooling
- Background scheduler runs daily
- Session cleanup runs hourly
- Holiday fetch runs daily

## Future Enhancements

The following features are planned but not yet implemented:
- API authentication (currently unauthenticated)
- More OAuth providers
- Team-specific holiday calendars
- Advanced reporting
- Mobile app integration

## References

- **Main Documentation**: `README.md`
- **Holiday Implementation**: `HOLIDAY_IMPLEMENTATION.md`
- **SQLC Migration**: `SQLC_MIGRATION_GUIDE.md`
- **Auth Setup**: `AUTH_SETUP.md`
- **Consolidated Reference**: `CONSOLIDATED_REFERENCE.md`
