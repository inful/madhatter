# Support Rota System

A comprehensive support duty management system with automatic scheduling, leave management, calendar subscriptions, and OAuth2 authentication.

## Features

### Core Functionality
- **Round-robin scheduling**: Automatic assignment of support duties with fair distribution
- **Leave management**: Unified system for sick leave, vacation, and other absences
- **Automatic cover assignment**: System automatically assigns covers when team members are on leave
- **Calendar subscriptions**: Personal ICS calendar URLs for any calendar app
- **Holiday support**: Automatic exclusion of holidays from scheduling (via iCal feeds)

### User Interface
- **Web dashboard**: HTMX-based responsive user interface
- **REST API**: HUMA-based API with automatic OpenAPI documentation
- **CLI tools**: Kong-based command-line interface for all operations

### Authentication & Security
- **OAuth2 authentication**: Support for Forgejo and GitLab providers
- **Session management**: Secure token hashing (SHA-256) and encryption (AES-256-GCM)
- **Role-based access**: Admin and Regular user roles
- **Development mode**: Fake OAuth provider for local testing

### Advanced Features
- **Automatic schedule maintenance**: 14-day rolling schedule maintained automatically
- **Static scheduling**: Preserves existing assignments, only fills gaps
- **Event-driven updates**: Schedule updates triggered on team changes, leave reports
- **Dual-mode generation**: Fill gaps vs. regenerate from scratch
- **Presence tracking**: Visual indicators for who's available and on leave

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

#### Required for Production
```bash
# OAuth2 Providers (at least one)
FORGEJO_CLIENT_ID=your-client-id
FORGEJO_CLIENT_SECRET=your-client-secret
FORGEJO_REDIRECT_URL=http://your-domain:8080/auth/callback?provider=forgejo

GITLAB_CLIENT_ID=your-client-id
GITLAB_CLIENT_SECRET=your-client-secret
GITLAB_REDIRECT_URL=http://your-domain:8080/auth/callback?provider=gitlab

# Session Security
SESSION_SECRET=generate-a-random-secret-key
```

#### Optional Configuration
```bash
# Meetings calendar
MEETINGS_TIMEZONE=Europe/Oslo
MEETINGS_TEAMS_URL=https://teams.example.com/meet

# Optional: override meeting descriptions using Go templates.
# The files are read by the server process at runtime.
MEETINGS_TEMPLATE_TEXT_PATH=/etc/support-rota/templates/meeting_description.txt.tmpl
MEETINGS_TEMPLATE_HTML_PATH=/etc/support-rota/templates/meeting_description.html.tmpl

# Optional: extra links to include in meeting event descriptions.
# Comma-separated; each item is either a raw HTML <a ...>...</a> or a Label|URL pair.
MEETINGS_LINKS='Runbook|https://example.com/runbook, <a href="https://example.com/raw">Raw link</a>'

# Holiday Service
HOLIDAY_URLS=https://www.officeholidays.com/subscribe/norway,https://www.officeholidays.com/subscribe/uk
HOLIDAY_FETCH_INTERVAL=24  # hours
HOLIDAY_LOOKAHEAD=365      # days

# OAuth URLs (for self-hosted providers)
FORGEJO_AUTH_URL=/login/oauth/authorize
FORGEJO_TOKEN_URL=/login/oauth/access_token
FORGEJO_USERINFO_URL=/api/v1/user
FORGEJO_SCOPE=read:user

GITLAB_AUTH_URL=https://gitlab.com/oauth/authorize
GITLAB_TOKEN_URL=https://gitlab.com/oauth/token
GITLAB_USERINFO_URL=https://gitlab.com/api/v4/user
GITLAB_SCOPE=read_user
```

### Meeting template overrides

The meetings calendar feed supports overriding the per-event descriptions via Go templates.

- Text description: `MEETINGS_TEMPLATE_TEXT_PATH` (uses `text/template`).
- HTML alternative description (Outlook-friendly): `MEETINGS_TEMPLATE_HTML_PATH` (uses `html/template`).

Both templates receive the same data:

- `MeetingName` (string)
- `TeamsURL` (string; only `http`/`https` URLs are passed through, otherwise empty)
- `Links` ([]struct)
	- `Label` (string)
	- `URL` (string)
	- `HTML` (HTML; only set when provided as raw `<a ...>`)
	- `Text` (string; suitable for plain-text output)
- `Present` ([]string)
- `Away` ([]string)
- `Support` (string)
- `Shuffle` ([]string)
- `Agenda` ([]string)

Notes:

- The HTML template should output a HTML fragment (it will be wrapped in `<html><body>...</body></html>` in the ICS).
- Long lines in ICS files may be folded (RFC 5545). This is normal.
- `MEETINGS_LINKS` is treated as trusted deployment input; if you use raw HTML anchors, they are included as-is.

Example text template (`meeting_description.txt.tmpl`):

```gotemplate
{{.MeetingName}}

Present:
{{- if .Present }}
{{- range .Present }}- {{.}}
{{- end }}
{{- else }}- (none)
{{- end }}
```

Example HTML template (`meeting_description.html.tmpl`):

```gotemplate
<h3>{{.MeetingName}}</h3>
{{- if .TeamsURL }}
<p><a href="{{.TeamsURL}}">Join Teams meeting</a></p>
{{- end }}
<h4>Present</h4>
<ul>
	{{- if .Present }}
	{{- range .Present }}<li>{{.}}</li>{{end}}
	{{- else }}
	<li>(none)</li>
	{{- end }}
</ul>

{{- if .Links }}
<h4>Links</h4>
<ul>
	{{- range .Links }}
	{{- if .HTML }}<li>{{.HTML}}</li>{{else}}<li><a href="{{.URL}}">{{.Label}}</a></li>{{end}}
	{{- end }}
</ul>
{{- end }}
```

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