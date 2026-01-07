# AGENTS.md

This file provides guidance to agents when working with code in this repository.

## Non-Obvious Project-Specific Information

**Database**: Uses modernc.org/sqlite (pure Go SQLite) with foreign keys explicitly enabled via PRAGMA. Database file is `support_rota.db`.

**CLI Framework**: Uses `github.com/alecthomas/kong` for CLI parsing. Commands are defined in a struct hierarchy and parsed with `kong.Parse(&CLI)`.

**API Framework**: Uses `github.com/danielgtaylor/huma/v2` with `go-chi/chi` adapter. All API operations must be registered via `huma.Register()`.

**Date Handling**: All dates use Go's reference format `"2006-01-02"` for parsing and formatting. Database stores dates as DATE strings.

**Round-Robin Scheduling**: The rota engine uses round-robin assignment with weekend skipping and cover assignment logic. Covers reference original assignments via `original_assignment_id` foreign key.

**Calendar ICS Generation**: ICS files are generated with 0o600 permissions. Calendar subscriptions use UUID tokens stored in `calendar_subscriptions` table.

**Test Structure**: Tests are co-located with source files (e.g., `db_test.go` next to `db.go`) and use testify assertions.

**Linter Configuration**: Uses golangci-lint v2 with extensive rules including shadow variable detection, performance checks, and custom tag alignment rules.

**SQLC Migration**: Database layer uses sqlc for type-safe SQL generation. Key files:
- `sqlc.yaml` - sqlc configuration
- `internal/database/sqlc/` - Generated type-safe code
- `internal/database/sqlc_wrapper.go` - Backward compatibility wrapper
- `SQLC_MIGRATION_GUIDE.md` - Detailed migration documentation

**SQLC Commands**:
- Generate code: `export PATH=$PATH:$(go env GOPATH)/bin && sqlc generate`
- Run tests: `go test ./internal/database -v`

**Critical Gotchas**:
- Foreign keys must be enabled manually with `PRAGMA foreign_keys = ON`
- Leave assignment creates cover records that reference original assignments
- Weekend dates are automatically skipped in scheduling
- Calendar subscriptions require creating a token entry before generating ICS feeds
- SQLC generates `time.Time` for dates, but your existing code uses string dates - use the wrapper for compatibility
- SQLC uses `sql.NullInt64` for nullable integers - wrapper converts to/from bool