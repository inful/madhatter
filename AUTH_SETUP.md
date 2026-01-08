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

Create a configuration file `config.yaml` in the project root:

```yaml
# Server Configuration
server:
  address: ":8080"
  session_secret: "your-secret-key-change-in-production"

# OAuth2 Providers
oauth:
  # Base URL for your application (used for callback URLs)
  app_url: "http://localhost:8080"
  
  # Forgejo Configuration
  forgejo:
    enabled: true
    client_id: "your-forgejo-client-id"
    client_secret: "your-forgejo-client-secret"
    # Base URL of your Forgejo instance
    base_url: "https://git.example.com"
    # Optional: restrict to specific organizations
    allowed_organizations: []
  
  # GitLab Configuration
  gitlab:
    enabled: true
    client_id: "your-gitlab-client-id"
    client_secret: "your-gitlab-client-secret"
    # Base URL of your GitLab instance (https://gitlab.com for SaaS)
    base_url: "https://gitlab.com"
    # Optional: restrict to specific groups
    allowed_groups: []
```

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
   - **Scopes**: `read_api`, `read_user`
5. Click **Save application**
6. Copy the **Application ID** and **Secret** to your config

## Database Setup

The system automatically creates the required tables on first run. The schema includes:
- `users` - User accounts with OAuth2 metadata
- `sessions` - Active user sessions
- `oauth_tokens` - Stored OAuth2 tokens for each provider

## Running the Application

1. **Install dependencies**:
   ```bash
   go mod download
   ```

2. **Build the application**:
   ```bash
   go build -o madhatter
   ```

3. **Run with config**:
   ```bash
   ./madhatter --config config.yaml
   ```

4. **Access the application**:
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

### Production Deployment

1. **Use HTTPS**:
   ```yaml
   server:
     address: ":443"
   oauth:
     app_url: "https://your-domain.com"
   ```

2. **Strong Session Secret**:
   ```bash
   # Generate a strong secret
   openssl rand -base64 32
   ```

3. **Database Security**:
   - Keep `support_rota.db` in a secure location
   - Set appropriate file permissions (600)
   - Regular backups

4. **OAuth2 Secrets**:
   - Never commit secrets to version control
   - Use environment variables or secure secret management
   - Rotate secrets regularly

5. **Allowed Organizations/Groups**:
   - Restrict access to specific teams
   - Prevent unauthorized access

## Troubleshooting

### "Invalid redirect URI" Error
- Ensure the `app_url` in config matches your actual domain
- Check that the callback URL format is correct: `/auth/callback/{provider}`

### "User not authorized" Error
- Check if your user is in the allowed organizations/groups
- Verify the OAuth2 application has correct scopes

### Session Issues
- Clear browser cookies and try again
- Check that `session_secret` is set and consistent
- Verify the session database table exists

### Database Errors
- Ensure SQLite3 is available
- Check file permissions on the database file
- Verify foreign keys are enabled (system does this automatically)

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

**Important:** In the current version, the `/api/v1/*` HTTP API endpoints do **not** enforce authentication by themselves. Requests without any `Authorization` header or session cookie are accepted by the server.

If you expose these endpoints, you **must** protect them using external mechanisms such as:

- An authenticated reverse proxy (e.g. OAuth2/OIDC proxy, SSO gateway)
- Network-level controls (firewall rules, VPN, private network)

Future versions may add first-class API authentication (e.g. validating `Authorization: Bearer <session_token>` headers or session cookies on `/api/v1/*` routes). Until that is implemented in the server code, treat the HTTP API as unauthenticated and rely on external protection.
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