# OAuth2 Authentication Setup Guide

This guide explains how to configure and set up OAuth2 authentication for the MadHatter support rota system.

## Overview

The system supports OAuth2 authentication with multiple providers. Currently implemented providers:
- **Forgejo** (self-hosted Git service)
- **GitLab** (can be self-hosted or SaaS)

## Prerequisites

1. Go 1.21 or later
2. SQLite3 database
3. An OAuth2 application registered with your provider

## Configuration

All configuration is read from environment variables. The full env-var
table is in the [README](README.md#environment-variables) — the auth-specific
variables are summarised below.

## Setting Up OAuth2 Applications

### Forgejo

1. Navigate to your Forgejo instance
2. Go to **Settings** → **Applications** → **OAuth2 Applications**
3. Click **New Application**
4. Configure:
   - **Application Name**: MadHatter Rota
   - **Redirect URI**: `http://your-domain:8080/auth/callback/forgejo`
   - **Confidential**: Yes
5. Click **Create Application**
6. Copy the **Client ID** and **Client Secret** to your config

### GitLab

1. Navigate to GitLab (or your self-hosted instance)
2. Go to **User Settings** → **Applications**
3. Click **New application**
4. Configure:
   - **Name**: MadHatter Rota
   - **Redirect URI**: `http://your-domain:8080/auth/callback/gitlab`
   - **Scopes**: `read_api`, `read_user` (both required when using group restrictions)
5. Click **Save application**
6. Copy the **Application ID** and **Secret** to your config

#### Restricting Access to GitLab Group Members

You can optionally restrict authentication to members of a specific GitLab group or subgroup. This is useful when you want to ensure only authorized team members can access the application.

**Required OAuth Scopes**:
- `read_user` - To read user profile information
- `read_api` - Required for group membership validation (automatically included when `allowed_group` is set)

**Configuration via Environment Variables**:
```bash
export GITLAB_CLIENT_ID="your-application-id"
export GITLAB_CLIENT_SECRET="your-secret"
export GITLAB_REDIRECT_URL="http://your-domain:8080/auth/callback?provider=gitlab"
export GITLAB_ALLOWED_GROUP="myorg/myteam"  # Optional: restrict to group members
# Note: When GITLAB_ALLOWED_GROUP is set, scope automatically includes "read_api read_user"
```

**Group Path Format**:
- Top-level group: `myorg`
- Subgroup: `myorg/myteam`
- Nested subgroup: `myorg/division/team`

**Behavior**:
- If `allowed_group` is **not set** (empty string): All users who can authenticate with GitLab are allowed
- If `allowed_group` **is set**: Only users who are members of the specified group can authenticate
- Users not in the group will see an authentication error message

**Important Notes**:
- The user must be a **direct member** of the specified group (minimum access level: Guest/10)
- Group membership is checked at authentication time via the GitLab API
- The OAuth application must have both `read_user` and `read_api` scopes when using group restrictions
- Group path is case-sensitive and must exactly match the group's full path in GitLab

## Database Setup

The system automatically creates the required tables on first run. The schema includes:
- `users` - User accounts with OAuth2 metadata
- `sessions` - Active user sessions (tokens are hashed using SHA-256)
- `oauth_tokens` - Stored OAuth2 tokens (encrypted at rest using AES-256-GCM)

**Security Notes**:
- Session tokens are never stored in plaintext; only SHA-256 hashes are stored
- OAuth access/refresh tokens are encrypted before storage and decrypted on retrieval
- Database backups are safe from session hijacking (hashed tokens can't be used directly)
- OAuth tokens require the `TOKEN_ENCRYPTION_KEY` to decrypt from backups

## Running the Application

### Development Mode (No OAuth Required)

For local development and testing, you can use the built-in development mode:

```bash
# Build the application
go build -o support-rota

# Run in development mode
./support-rota serve --port 8080 --development
```

**Development Mode Features**:
- Bypasses full OAuth setup
- Uses fake OAuth provider
- Automatically creates admin user
- Special development login page at `/login`
- No external OAuth provider configuration needed

**Access**: Navigate to `http://localhost:8080` and click login.

### Production Mode

1. **Install dependencies**:
    ```bash
    go mod download
    ```

2. **Build the application**:
    ```bash
    go build -o support-rota
    ```

3. **Set environment variables** (production only):
    ```bash
    # Required for production: encryption key for OAuth tokens
    export TOKEN_ENCRYPTION_KEY=$(openssl rand -base64 32)
    # Required: strong session secret
    export SESSION_SECRET=$(openssl rand -base64 32)
    ```
    
    > **Note**: Without `TOKEN_ENCRYPTION_KEY`, a random key is generated at startup, which means OAuth tokens won't survive application restarts.

4. **Run the server**:
    ```bash
    ./support-rota serve --port 8080
    ```

6. **Access the application**:
    - Navigate to `http://localhost:8080`
    - Click **Login** to see available providers
    - Choose your provider and authorize the application

## User Management

### First Login
- The first user to log in automatically becomes an **admin**
- Admins can manage team members and generate schedules

### Admin Privileges
Admins can:
- Add/remove team members
- Generate schedules
- Manage leave requests
- Create calendar subscriptions
- Access all team data

### Regular Users
Regular users can:
- View schedules
- Report their own leave
- Create personal calendar subscriptions

## Security Considerations

### Built-in Security Features

The system implements several security measures to protect sensitive data:

1. **Session Token Hashing**:
   - Session tokens are hashed using SHA-256 before storage
   - Only token hashes are stored in the database
   - Database compromise does not expose live session tokens
   - Tokens are validated by comparing hashes

2. **OAuth Token Encryption**:
   - OAuth access and refresh tokens are encrypted at rest using AES-256-GCM
   - Encryption key is loaded from `TOKEN_ENCRYPTION_KEY` environment variable
   - Tokens are encrypted before storage and decrypted on retrieval
   - Random nonce for each encryption ensures semantic security

### Production Deployment

1. **Use HTTPS** — front the application with a TLS-terminating reverse
   proxy (Caddy, nginx, traefik). The server itself speaks plain HTTP on
   the configured port; set the proxy's listener to `:443` and forward
   to the support-rota port internally. The application emits
   `Strict-Transport-Security` headers on HTTPS connections (see
   [README: Authentication & Security](../README.md#authentication--security)).

2. **Strong Session Secret**:
   ```bash
   # Generate a strong secret
   openssl rand -base64 32
   ```

3. **Token Encryption Key** (Required for Production):
   ```bash
   # Generate a 32-byte encryption key (base64 encoded)
   export TOKEN_ENCRYPTION_KEY=$(openssl rand -base64 32)
   ```
   
   **Important**: 
   - Store this key securely (e.g., secret manager, vault)
   - Never commit the key to version control
   - If the key is lost, all encrypted OAuth tokens become unrecoverable
   - Key rotation is not currently automated; users must re-authenticate after key changes
   - Without this key, a random key is generated on startup (tokens won't survive restarts)

4. **Database Security**:
   - Keep `support_rota.db` in a secure location
   - Set appropriate file permissions (600)
   - Regular backups
   - Session tokens are hashed (database compromise doesn't expose live sessions)
   - OAuth tokens are encrypted (requires `TOKEN_ENCRYPTION_KEY` to decrypt)

5. **OAuth2 Secrets**:
   - Never commit secrets to version control
   - Use environment variables or secure secret management
   - Rotate secrets regularly

6. **Allowed Organizations/Groups**:
   - Restrict access to specific teams
   - Prevent unauthorized access

## Troubleshooting

### "Invalid redirect URI" Error
- Ensure the `app_url` in config matches your actual domain
- Check that the callback URL format is correct: `/auth/callback/{provider}`

### "User not authorized" Error
- Check if your user is in the allowed organizations/groups
- Verify the OAuth2 application has correct scopes (for GitLab: `read_api`, `read_user`)
- For GitLab group restrictions: Ensure the user is a direct member of the specified group
- Verify the `allowed_group` path matches the group's full path in GitLab (case-sensitive)

### Session Issues
- Clear browser cookies and try again
- Check that `session_secret` is set and consistent
- Verify the session database table exists

### Database Errors
- Ensure SQLite3 is available
- Check file permissions on the database file
- Verify foreign keys are enabled (system does this automatically)

### Encryption Errors
- **"Failed to decrypt access token"**: The `TOKEN_ENCRYPTION_KEY` environment variable has changed or is missing
  - Ensure the same key is used across application restarts
  - If key is lost, OAuth tokens in database cannot be decrypted
  - **User Impact**: Affected users will be automatically logged out and must re-authenticate
  - To recover: Set the correct key or clear OAuth tokens from the database
- **"Encryption key must be 32 bytes"**: The `TOKEN_ENCRYPTION_KEY` must decode to exactly 32 bytes when base64 decoded
  - Generate a proper key: `openssl rand -base64 32` (produces 44 characters that decode to 32 bytes)
- **Warning about random encryption key**: `TOKEN_ENCRYPTION_KEY` not set
  - Set the environment variable for production deployments
  - Without it, tokens won't survive application restarts
  - **User Impact**: Users will need to re-authenticate after each application restart

## Development

### Testing OAuth2 Locally
For local development, you can use:
- **ngrok** to expose localhost: `ngrok http 8080`
- **localhost.run** for SSH tunneling
- **OAuth2 proxy** for testing multiple providers

### Adding New Providers
To add a new OAuth2 provider:

1. Create a new file in `internal/auth/providers/`
2. Implement the `Provider` interface
3. Add provider to `internal/auth/providers/providers.go`
4. Update configuration loading
5. Add callback route

Example provider structure:
```go
type Provider struct {
    Name         string
    ClientID     string
    ClientSecret string
    BaseURL      string
}

func (p *Provider) GetAuthURL(state string) string {
    // Return authorization URL
}

func (p *Provider) ExchangeCode(code string) (string, error) {
    // Exchange authorization code for token
}

func (p *Provider) GetUserEmail(token string) (string, error) {
    // Get user email from provider API
}
```

## API Authentication

Every `/api/v1/*` endpoint requires an authenticated user. Two schemes are accepted on every protected endpoint:

- **`sessionAuth`** — the same `session_token` cookie the web UI uses. Log in via OAuth, send the resulting cookie.
- **`apiTokenAuth`** — `Authorization: Bearer <token>` where the token was generated via `POST /api/v1/tokens/generate` while logged in as a user (admin or not).

Both schemes are declared on every protected operation in `internal/api/operations.go`; see the OpenAPI document served at `GET /docs` for the full list. Use the bearer header for shell scripts and CI; use the cookie for browser-driven flows.

**Exceptions** — the following endpoints are public:

- `GET /api/v1/holidays`
- `GET /api/v1/holidays/status`
- `POST /api/v1/holidays/refresh`
- `GET /api/v1/calendar/{token}/ics` and the related `/calendar/{token}/{team.ics,meetings.ics,meetings/{date}.html}` calendar feeds (the per-subscription UUID in the path is the auth — rotating it revokes access).

Every handler still calls `auth.GetUserFromContext(ctx)` and refuses with `401 Unauthorized` when no user is present, so a missing session is a hard error — there is no permissive "anonymous request" path on the protected endpoints.

### Generating an API token

```bash
# Log in via the web UI to obtain a session cookie, then:
curl -X POST http://localhost:8080/api/v1/tokens/generate \
  -H "Cookie: session_token=<your-session-cookie>" \
  -H "Content-Type: application/json" \
  -d '{"name": "my-cli-token", "expires_in_days": 30}'

# Response
{
  "token": "rota_api_...",  # shown ONCE — store it now
  "id":    "<token-uuid>",
  "expires_at": "2026-10-28T..."
}
```

Use the returned token as `Authorization: Bearer rota_api_...`. List your tokens with `GET /api/v1/tokens` and revoke with `DELETE /api/v1/tokens/{id}`.

## Calendar Subscriptions

Authenticated users can create calendar subscriptions:
1. Navigate to **Calendar Subscriptions**
2. Select team member
3. Copy the generated ICS URL
4. Add to your calendar app

Calendar URLs include authentication tokens and are valid until revoked.

## Support

For issues or questions:
1. Check the logs for error messages
2. Verify configuration syntax
3. Test OAuth2 flow manually
4. Check database schema matches expected structure

## Migration from Non-Authenticated Mode

If upgrading from a version without authentication:

1. **Backup your database**:
   ```bash
   cp support_rota.db support_rota.db.backup
   ```

2. **Add auth schema**:
   The system will automatically add required tables on startup.

3. **Create admin user**:
   - Log in with your OAuth2 provider
   - The first user becomes admin automatically

4. **Verify existing data**:
   - Team members, schedules, and leave records remain intact
   - All features continue to work normally