# AGENTS.md

This file provides guidance to agents when working with code in this repository.

## Non-Obvious Project-Specific Information

**Database**: Uses modernc.org/sqlite (pure Go SQLite) with foreign keys explicitly enabled via PRAGMA. Database file is `support_rota.db`.

**CLI Framework**: Uses `github.com/alecthomas/kong` for CLI parsing. Commands are defined in a struct hierarchy and parsed with `kong.Parse(&CLI)`.

**API Framework**: Uses `github.com/danielgtaylor/huma/v2` with `go-chi/chi` adapter. All API operations must be registered via `huma.Register()`.

**Date Handling**: All dates use Go's reference format `"2006-01-02"` for parsing and formatting. Database stores dates as DATE strings.

**Round-Robin Scheduling**: The rota engine uses round-robin assignment with weekend skipping and cover assignment logic. Covers reference original assignments via `original_assignment_id` foreign key.

**Automatic Schedule Maintenance**: The system automatically maintains a 14-day rolling schedule using `ScheduleMaintenance` service in `internal/rota/maintenance.go`. Key features:
- `EnsureSchedule()` - Creates 14-day rolling schedule automatically
- `GenerateMissingDays()` - Fills gaps while preserving existing assignments ("static as possible" scheduling)
- `RegenerateSchedule()` - Deletes and recreates schedule from scratch
- `HandleTeamChange()` - Updates schedule when team members change
- `HandleLeaveChange()` - Creates cover assignments when members take leave
- Web handlers automatically trigger schedule maintenance on key events

**Calendar ICS Generation**: ICS files are generated with 0o600 permissions. Calendar subscriptions use UUID tokens stored in `calendar_subscriptions` table.

**Test Structure**: Tests are co-located with source files (e.g., `db_test.go` next to `db.go`) and use testify assertions.

**Linter Configuration**: Uses golangci-lint v2 with extensive rules including shadow variable detection, performance checks, and custom tag alignment rules. Cyclomatic complexity limit is 10.

**SQLC Migration**: Database layer uses sqlc for type-safe SQL generation. Key files:
- `sqlc.yaml` - sqlc configuration
- `internal/database/sqlc/` - Generated type-safe code
- `internal/database/sqlc_wrapper.go` - Backward compatibility wrapper
- `SQLC_MIGRATION_GUIDE.md` - Detailed migration documentation

**SQLC Commands**:
- Generate code: `export PATH=$PATH:$(go env GOPATH)/bin && sqlc generate`
- Run tests: `go test ./internal/database -v`

**Database Methods**: New methods added for schedule maintenance:
- `GetAssignmentsByDateRange()` - Get assignments within date range
- `GetLatestAssignmentDate()` - Find most recent assignment date
- `DeleteAssignmentsInRange()` - Delete assignments in range for regeneration

**Critical Gotchas**:
- Foreign keys must be enabled manually with `PRAGMA foreign_keys = ON`
- Leave assignment creates cover records that reference original assignments
- Weekend dates are automatically skipped in scheduling
- Calendar subscriptions require creating a token entry before generating ICS feeds
- SQLC generates `time.Time` for dates, but your existing code uses string dates - use the wrapper for compatibility
- SQLC uses `sql.NullInt64` for nullable integers - wrapper converts to/from bool
- **Static Scheduling**: `GenerateMissingDays()` preserves existing assignments and only fills gaps
- **Template Compatibility**: Dashboard template handles string dates directly (no `.Format` calls)
- **No Duplicate Days**: Only one assignment per day (original or cover, not both)

**Code Quality Standards**:
- **Linter**: Uses golangci-lint v2 with 0 issues allowed
- **Cyclomatic Complexity**: Limited to 10 per function
- **Formatting**: gofumpt compliant
- **Comments**: All comments end with periods (godot)
- **Tests**: All use testify assertions (testifylint compliant)
- **Web Templates**: Include copy functionality for URLs, visual feedback notifications

**Web Interface Features**:
- **Calendar Subscriptions**: Copy button with clipboard API and visual notification
- **Schedule Display**: Shows cover badges and leave indicators
- **Automatic Maintenance**: Triggers on team changes, leave reports, and page loads

**Authentication System**: OAuth2-based authentication with multiple providers
- **Providers**: Forgejo and GitLab (extensible to others)
- **Session Management**: Secure session tokens stored in database
- **User Roles**: Admin (first login) and Regular users
- **Middleware**: `RequireAuth` and `RequireAdmin` for route protection
- **Database Tables**: `users`, `sessions`, `oauth_tokens`
- **Configuration**: YAML-based OAuth provider settings
- **Admin Features**: Team management, schedule generation, leave approval
- **User Features**: View schedules, report leave, calendar subscriptions

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