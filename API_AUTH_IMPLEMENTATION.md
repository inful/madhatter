# API Authentication Implementation

## Overview

This document describes the authentication layer implementation for the Support Rota System API. The system now supports both session-based authentication (for web interface) and token-based authentication (for API access).

## Architecture

### Dual Authentication Methods

1. **Session-Based Authentication** (Web Interface)
   - Uses secure HTTP cookies
   - SHA-256 token hashing with hex encoding
   - 24-hour expiration
   - OAuth2 flow integration

2. **Token-Based Authentication** (API Access)
   - Uses Bearer tokens in Authorization header
   - SHA-256 hashing for database storage
   - Flexible expiration (never expires by default)
   - Tokens shown only once during generation

## Database Schema

### New Table: `api_tokens`

```sql
CREATE TABLE api_tokens (
    id TEXT PRIMARY KEY,                 -- UUID, not an integer
    user_id TEXT NOT NULL,
    name TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    is_active INTEGER DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME,
    last_used_at DATETIME,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
```

**Key Features:**
- `token_hash`: SHA-256 hash for quick lookup and security
- `is_active`: Boolean flag to enable/disable token without deletion
- `last_used_at`: Tracks token usage for security auditing
- `expires_at`: Optional expiration for temporary tokens

## Implementation Components

### 1. SQLC Queries (`internal/database/sqlc/queries/api_tokens.sql`)

```sql
-- name: CreateAPIToken :execresult
-- name: GetAPITokenByHash :one
-- name: ListAPITokensByUser :many
-- name: UpdateAPITokenLastUsed :exec
-- name: DeleteAPIToken :exec
-- name: DeleteExpiredTokens :exec
```

### 2. API Token Manager (`internal/auth/handlers.go`)

**Methods:**
- `GenerateAPIToken(user, name, expiry)`: Creates new token
- `ListAPITokens(user)`: Lists user's tokens
- `RevokeAPIToken(user, tokenID)`: Deletes token
- `ValidateAPIToken(token)`: Validates and returns user

**Token Format:**
- UUID-based token IDs (stored as TEXT PRIMARY KEY)
- Token hash: SHA-256 hex-encoded string

### 3. Authentication Middleware

**Session Middleware** (`internal/auth/middleware.go`):
```go
func RequireAuth(next http.Handler) http.Handler
func RequireAdmin(next http.Handler) http.Handler
func OptionalAuth(next http.Handler) http.Handler
```

**API Token Authentication:**
The system uses `OptionalAuth` middleware combined with handler-level authentication checks. API tokens are validated by:
1. Checking the Authorization header for Bearer tokens
2. Hashing the token with SHA-256
3. Looking up the hash in the database
4. Adding user info to context if valid

**Usage:**
```go
// For web routes (session-based)
router.With(middleware.RequireAuth).Get("/api/team", handlers.GetTeam)

// For API routes (token or session-based)
router.With(middleware.OptionalAuth).Get("/api/v1/team", apiHandlers.GetTeam)
// Handler checks auth: auth.GetUserFromContext(ctx)
```

### 4. HUMA v2 API Endpoints (`internal/api/operations.go`)

**Token Management:**
- `POST /api/v1/tokens/generate` - Generate a new API token (returns the plaintext once)
- `GET /api/v1/tokens` - List tokens owned by the authenticated user
- `DELETE /api/v1/tokens/{id}` - Revoke a token
- `POST /api/v1/tokens/cleanup` - Hard-delete tokens whose `expires_at` is in the past (admin)

**Protected Endpoints (representative):**
- `GET /api/v1/team` - List team members
- `POST /api/v1/team` - Add team member (admin)
- `PUT /api/v1/team/{id}` - Update member (admin)
- `DELETE /api/v1/team/{id}` - Delete member (admin)
- `GET /api/v1/presence/today` - Today's on-site / leave / WFH roster
- `POST /api/v1/leave` - Report leave (triggers auto-cover)
- `GET /api/v1/leave` - List leave records
- `PUT /api/v1/leave/{id}` - Update leave (admin)
- `DELETE /api/v1/leave/{id}` - Delete leave (admin)
- `POST /api/v1/schedule/generate` - Regenerate the schedule for a date range (admin)
- `POST /api/v1/calendar/subscribe` - Create a calendar subscription
- `GET /api/v1/wfh`, `POST /api/v1/wfh`, `DELETE /api/v1/wfh/{id}`, `POST /api/v1/wfh/{id}/withdraw` (admin)
- `POST /api/v1/wfh/report-today`, `POST /api/v1/wfh/settle` (admin), `GET /api/v1/wfh/quota`, `GET /api/v1/wfh/date/{date}` (admin)
- `GET /api/v1/swaps`, `POST /api/v1/swaps`, `POST /api/v1/swaps/{id}/{accept,reject,cancel}`, `DELETE /api/v1/swaps/{id}` (admin)

The full list is in `internal/api/operations.go` and is also published as the live OpenAPI document at `GET /docs` (HUMA generates this from the same source of truth — the doc-table here is a summary, not a contract).

**Public endpoints (no auth required):**
- `GET /api/v1/holidays`
- `GET /api/v1/holidays/status`
- `POST /api/v1/holidays/refresh`
- `GET /api/v1/calendar/{token}/ics` and the related `/calendar/{token}/{team.ics,meetings.ics,meetings/{date}.html}` calendar feeds (the per-subscription UUID in the path is the auth — rotating it revokes access).

## Usage Examples

### 1. Generate API Token

```bash
# Log in via the web UI first to obtain a session cookie, then:
curl -X POST http://localhost:8080/api/v1/tokens/generate \
  -H "Cookie: session_token=<session-cookie>" \
  -H "Content-Type: application/json" \
  -d '{"name": "my-cli-token", "expires_in_days": 30}'

# Response
{
  "token": "rota_api_abc123...",   # shown ONCE — store it now
  "id":    "<token-uuid>",          # TEXT PRIMARY KEY (UUID), not an integer
  "expires_at": "2026-10-28T..."
}
```

If you don't have a session cookie handy, the bearer token of an
existing admin token works just as well:

```bash
curl -X POST http://localhost:8080/api/v1/tokens/generate \
  -H "Authorization: Bearer rota_api_<existing-token>" \
  -H "Content-Type: application/json" \
  -d '{"name": "ci-token", "expires_in_days": 0}'
```

### 2. Use API Token

```bash
# List team members
curl http://localhost:8080/api/v1/team \
  -H "Authorization: Bearer rota_api_abc123..."

# Response
{
  "team": [
    {"id": 1, "name": "Alice", "email": "alice@example.com"}
  ]
}
```

### 3. Revoke Token

```bash
curl -X DELETE http://localhost:8080/api/v1/tokens/123 \
  -H "Cookie: session_token=<session_hash>"
```

## Security Features

### 1. Token Storage
- **Hash**: SHA-256(token) stored in database for lookup
- **Never stored in plain text**
- Tokens are only shown once during generation

### 2. Token Validation
```go
func ValidateAPIToken(ctx context.Context, db *database.DB, token string) (*User, error) {
    // 1. Hash token
    hash := sha256.Sum256([]byte(token))
    hashHex := hex.EncodeToString(hash[:])
    
    // 2. Lookup in database
    dbToken, err := db.GetAPITokenByHash(hashHex)
    
    // 3. Check if active
    if !dbToken.IsActive.Valid || dbToken.IsActive.Int64 != 1 {
        return nil, ErrTokenInactive
    }
    
    // 4. Check expiration
    if dbToken.ExpiresAt.Valid && time.Now().After(dbToken.ExpiresAt.Time) {
        return nil, ErrTokenExpired
    }
    
    // 5. Update last used
    db.UpdateAPITokenLastUsed(dbToken.ID)
    
    return db.GetUser(dbToken.UserID)
}
```

### 3. Token Hashing
```go
func hashToken(token string) string {
    hash := sha256.Sum256([]byte(token))
    return hex.EncodeToString(hash[:])
}
```

## Environment Variables

### Required for Production

```bash
# Session secret (minimum 32 bytes)
export SESSION_SECRET="your-very-strong-secret-key-here"
```

### Development Mode

```bash
# No secrets needed in development mode
./support-rota serve --port 8080 --development
```

## Integration with Existing System

### 1. Database Compatibility
- Uses existing SQLite database
- Foreign key relationships to `users` table
- Compatible with SQLC generated code

### 2. Session Management
- Shares same `AuthManager` structure
- Uses existing user service
- Compatible with OAuth2 user accounts

### 3. Web Interface
- Existing web handlers unchanged
- New API endpoints added alongside
- No breaking changes to existing functionality

### 4. HUMA v2 Integration
- All API endpoints registered via `huma.Register()`
- OpenAPI documentation auto-generated
- Proper error responses and status codes

## Testing

### Integration Tests
```go
func TestHUMAAPIIntegration(t *testing.T) {
    // Test token generation
    // Test token usage
    // Test token revocation
    // Test authentication failures
}
```

### Security Tests
```go
func TestAPITokenSecurity(t *testing.T) {
    // Test encryption/decryption
    // Test token hashing
    // Test expiration handling
    // Test invalid token rejection
}
```

## Deployment Considerations

### 1. Production Requirements
- Strong `SESSION_SECRET` (32+ bytes)
- HTTPS for all API endpoints
- Secure database file permissions (600)

### 2. Token Lifecycle Management
- Regular cleanup of expired tokens
- Monitor `last_used_at` for suspicious activity
- Implement token rotation policies
- Disable tokens with `is_active` flag instead of deletion

### 3. Monitoring
- Log token generation events
- Log authentication failures
- Track token usage patterns

## Roadmap

### Shipped since this doc was written

- **Multiple OAuth providers** — Forgejo and GitLab are both wired through per-provider env vars (`FORGEJO_*`, `GITLAB_*`).
- **IP-based rate limiting** — `internal/ratelimit/` throttles `/auth/login/{provider}` and `/api/v1/tokens/*`; per-IP, configurable via env. Per-token rate limiting is not implemented.
- **API surface** — leave CRUD, schedule read/write, holiday queries, calendar subscription, full WFH lifecycle (ad-hoc, recurring, assigned, swap), hat-swap CRUD, presence query, and an admin database backup/restore surface are all available under `/api/v1/*`. See `internal/api/operations.go` or `GET /docs` for the full list.
- **Backup / restore** — `/admin/database/backup` and `/admin/database/restore` (admin-only web routes; no API equivalents yet).

### Still planned

1. **Token Scopes** — limit token permissions (read-only, admin-only).
2. **Per-token rate limits** — currently per-IP only.
3. **Token rotation reminders** — operators must rotate by policy today.
4. **Per-token audit logging** — `last_used_at` is updated on every authenticated request, but a richer audit trail is not.

## Migration Guide

### Database migrations

Schema migrations are managed by `golang-migrate` and auto-apply on every `serve` startup via `database.RunMigrations()`. The `api_tokens` table is created by the initial schema migration `000001_initial_schema.up.sql`; no manual SQL load is required. The source of truth for the schema is the `migrations/` directory; `internal/database/sqlc/schema.sql` is a single-file snapshot used by SQLC for code generation. Inspect migration state with:

```bash
./support-rota migrate status
```

### First-time setup

1. **Set environment variables**
   ```bash
   export SESSION_SECRET="$(openssl rand -base64 32)"
   export TOKEN_ENCRYPTION_KEY="$(openssl rand -base64 32)"
   ```

2. **Start the server**
   ```bash
   ./support-rota serve --port 8080
   ```
   Migrations apply on first boot.

3. **Log in via OAuth** (or `--development` for the fake provider). The first user becomes admin.

4. **Generate an API token** for scripts/CI via the web UI (User menu → API tokens) or directly with `curl` against `POST /api/v1/tokens/generate` — see the example above.

### Backward compatibility

- Existing web interface unchanged.
- Existing database data preserved (migrations are additive).
- The three public endpoints (`/api/v1/holidays`, `/api/v1/holidays/status`, `/api/v1/holidays/refresh`) and the per-token calendar feeds continue to accept anonymous traffic; every other `/api/v1/*` endpoint requires authentication. There is no "optional" toggle.

## Security Best Practices

### 1. Token Handling
- Never log full tokens
- Store only hashed/encrypted tokens
- Use HTTPS in production
- Implement rate limiting

### 2. User Management
- First user becomes admin automatically
- Admin privileges required for sensitive operations
- Regular user role for standard operations

### 3. Session Security
- 24-hour session expiration
- HttpOnly cookies
- SameSite strict mode
- Secure flag in production

### 4. Token Security
- Optional expiration dates
- Last-used tracking
- Easy revocation
- No plain-text storage

## Conclusion

The API authentication layer provides:

✅ **Security**: SHA-256 hashing, secure token storage  
✅ **Flexibility**: Session and token-based auth  
✅ **Compatibility**: Works with existing system  
✅ **Usability**: Simple token generation and management  
✅ **Auditability**: Usage tracking and logging  
✅ **Extensibility**: Easy to add new features  

The implementation is production-ready and follows security best practices while maintaining compatibility with the existing Support Rota System.