# AGENTS.md

This file provides guidance to agents when working with code in this repository.

## Project Overview

**Support Rota System** - A comprehensive support duty management system with automatic scheduling, leave management, calendar subscriptions, and OAuth2 authentication.

## Technology Stack

- **Language**: Go 1.25
- **API Framework**: HUMA v2 with go-chi/chi router
- **Web Framework**: HTMX (server-side rendering)
- **Database**: SQLite (modernc.org/sqlite)
- **CLI Framework**: Kong
- **Authentication**: OAuth2 (Forgejo, GitLab)
- **Calendar Format**: ICS (iCalendar)
- **SQL Generation**: sqlc for type-safe SQL
- **Linter**: golangci-lint version v2.8.0

## Non-Obvious Project-Specific Information

### Agent instructions

- When working with software libraries, API, third party tools, etc, first check with the context7 mcp for the most up to date documentations.
- For anything that involves complex analysis, planning and designing, use sequential thinking mcp.

### Database
- Uses `modernc.org/sqlite` (pure Go SQLite)
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
- Compatible with `modernc.org/sqlite` via generic `database/sqlite` driver

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

### Calendar ICS Generation
- ICS files are generated with 0o600 permissions
- Calendar subscriptions use UUID tokens stored in `calendar_subscriptions` table
- Uses `github.com/arran4/golang-ical` library

### Test Structure
- Tests are co-located with source files (e.g., `db_test.go` next to `db.go`)
- All tests use testify assertions
- Testifylint compliant
- **Use testing package helpers**: Always use `t.Setenv()` instead of `os.Setenv()` and `t.Chdir()` instead of `os.Chdir()` in tests
  - These helpers automatically clean up after tests
  - No need for manual defer cleanup functions
  - Prevents environment pollution between tests
  - Enforced by golangci-lint's `usetesting` rule

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
│   │   ├── ical.go                # iCal parsing
│   │   └── store.go               # In-memory storage
│   ├── calendar/
│   │   ├── ical.go                # ICS generation
│   │   └── ical_test.go           # Tests
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
5. **Documentation updated**: Check for outdated references
6. **No broken links**: Verify all file references in docs

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
- **Audit Report**: `documentation_audit.md`
- **Holiday Implementation**: `HOLIDAY_IMPLEMENTATION.md`
- **SQLC Migration**: `SQLC_MIGRATION_GUIDE.md`
- **Auth Setup**: `AUTH_SETUP.md`
- **Consolidated Reference**: `CONSOLIDATED_REFERENCE.md`