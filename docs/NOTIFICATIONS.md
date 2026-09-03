# Notifications

The Support Rota system sends email notifications to team members when
something happens that affects them. The architecture is designed to be
multi-channel-capable: today only **email** is wired; adding Slack,
Microsoft Teams, or an HTTP webhook later is a self-contained addition
to `internal/notify/channels/`.

## What fires when

| Event | Source | Recipient | When it fires |
|---|---|---|---|
| HAT swap requested | swap handler | target member | After a swap is created |
| HAT swap accepted | swap handler | requester + target | After a swap is accepted |
| HAT swap rejected | swap handler | requester | After a swap is rejected |
| HAT swap cancelled | swap handler | target | After a swap is cancelled by the requester |
| WFH swap requested | wfh swap handler | target member | After a WFH-swap is created (assigned / swap-origin rows only) |
| WFH swap accepted | wfh swap handler | requester | After the target accepts a WFH-swap |
| WFH swap rejected | wfh swap handler | requester | After the target rejects a WFH-swap |
| WFH swap cancelled | wfh swap handler | target | After the requester cancels a pending WFH-swap |
| WFH request approved | WFH service | requester | After settlement approves the request |
| WFH request denied | WFH service | requester | After settlement denies the request |
| WFH admin-withdrawn | admin handler | requester | After an admin withdraws an approved request |
| Cover assigned | rota engine | cover member | After a leave is processed and covers are assigned |

WFH self-cancel and self-withdraw are **not** notified — the user took
the action themselves.

Cover assignments emit **one** email per cover member per leave, with
the full date range. Recurring WFH materialization is silent (the user
configured the recurrence deliberately).

## Architecture

```
producer code (web handlers, WFH service, rota engine)
    │
    ▼ calls Notifier (non-blocking, never returns an error)
ChannelNotifier  (writes to outbox, then returns)
    │
    ▼ INSERT INTO notification_outbox
notification_outbox (SQLite)
    │
    ▼ polls every NOTIFY_OUTBOX_POLL_INTERVAL
Worker.Run (background goroutine)
    │
    ▼ dispatches to channel by name
Channel.Send  (SMTP, etc.)
```

Key properties:

- **Producers never block on network I/O.** Writing to the outbox is
  the only work they do; SMTP is the worker's problem.
- **SMTP outages don't lose notifications.** Failed rows are
  rescheduled with exponential backoff up to 1h, retried
  `NOTIFY_OUTBOX_MAX_ATTEMPTS` times, then marked `dead`.
- **One row per (event, recipient, channel).** Per-channel retry
  semantics stay clean and partial fan-out failures don't lose work.

## Configuration

All configuration is environment-driven. The defaults are tuned for
local development (log-only mode); production must set the email
variables explicitly.

### Base URL

| Variable | Default | Description |
|---|---|---|
| `NOTIFY_BASE_URL` | `http://localhost:8080` | Used in templates for "view in dashboard" links. May be the internal address. |
| `NOTIFY_PUBLIC_BASE_URL` | _(falls back to `NOTIFY_BASE_URL`)_ | Externally-visible origin used for absolute URLs in emails (one-click unsubscribe links). Should be the public HTTPS host users actually visit. In dev, leave unset to use the same value as `NOTIFY_BASE_URL`. |

### Unsubscribing

Members can opt out of email notifications with a one-click link in
every email body and on the email's `List-Unsubscribe` header
(RFC 8058). Both use the same HMAC-signed token, so the
`SESSION_SECRET` env var (already required at server startup) is
the only signing key in play.

The flow:

1. The renderer mints a per-recipient URL:
   `{NOTIFY_PUBLIC_BASE_URL}/unsubscribe?token={member_id}.{hmac}`.
   The token has no expiry; revocation happens by either rotating
   `SESSION_SECRET` or by the member re-enabling notifications.
2. The email channel stamps the same URL on
   `List-Unsubscribe: <URL>` and
   `List-Unsubscribe-Post: List-Unsubscribe=One-Click`. Mail
   clients that support RFC 8058 show a one-click "unsubscribe"
   button without opening the link.
3. The `/unsubscribe` handler verifies the HMAC, sets
   `notification_preferences.email_enabled = 0`, and renders a
   small confirmation page. Invalid tokens, missing tokens, and
   tokens for deleted members all show the same
   "link no longer valid" page so attackers can't tell signatures
   from typos.
4. `ChannelNotifier` consults `notification_preferences` on every
   dispatch. A disabled member is silently skipped (no outbox
   row written, no channel call). A transient DB blip on the
   preference read defaults to "enabled" so SMTP hiccups don't
   silently drop notifications.

The confirmation page links to `/unsubscribe/resume` (POST) so a
member who clicked by accident can re-enable without logging in.
The resume endpoint also verifies the token, so it's safe to expose
publicly.

The absence of a `notification_preferences` row is treated as
"enabled" (the default for new members). A single index on
`email_enabled = 0` keeps the disabled-row scan cheap; the common
path (enabled member, no row) stays at one query.

There is no admin override today: the only way to re-enable a
member who lost their token is to delete the
`notification_preferences` row directly in the database, or for
the member to use the resume link from the same email.

### Adding a new channel

### Channel list

| Variable | Default | Description |
|---|---|---|
| `NOTIFY_CHANNELS` | _(empty when email disabled)_ | Comma-separated channel names that should receive outbox rows. `email` is the only supported value in v1. |
| `NOTIFY_EMAIL_ENABLED` | `false` | When `true`, the email channel is registered. Required for any email delivery. |

### Email (SMTP)

| Variable | Default | Description |
|---|---|---|
| `NOTIFY_SMTP_HOST` | _(none — required when email enabled)_ | `host:port` of the SMTP server (e.g. `smtp.example.com:587`). |
| `NOTIFY_SMTP_USER` | _(empty)_ | SMTP authentication username. Empty = anonymous relay. |
| `NOTIFY_SMTP_PASSWORD` | _(empty)_ | SMTP authentication password. |
| `NOTIFY_SMTP_FROM` | `MadHatter Rota <noreply@example.com>` | The From: address (display name + email). |
| `NOTIFY_SMTP_IDENTITY` | _(empty)_ | `smtp.PlainAuth` identity. Empty defaults to the username. |

### Outbox worker

| Variable | Default | Description |
|---|---|---|
| `NOTIFY_OUTBOX_POLL_INTERVAL` | `30s` | How often the worker checks for due rows. |
| `NOTIFY_OUTBOX_MAX_ATTEMPTS` | `5` | After this many failures a row is marked `dead`. |
| `NOTIFY_OUTBOX_BACKOFF_BASE` | `30s` | First retry delay. Subsequent retries double, capped at 1h. |

### Template overrides

Each event has two templates: a subject and a body. Both are
`text/template` files. Operators can override any of them via env
var pointing at a file path:

| Variable | Template |
|---|---|
| `NOTIFY_SWAP_REQUESTED_TXT_PATH` | swap.requested body |
| `NOTIFY_SWAP_REQUESTED_SUBJECT_TXT_PATH` | swap.requested subject |
| `NOTIFY_SWAP_ACCEPTED_TXT_PATH` | swap.accepted body |
| `NOTIFY_SWAP_ACCEPTED_SUBJECT_TXT_PATH` | swap.accepted subject |
| `NOTIFY_SWAP_REJECTED_TXT_PATH` | swap.rejected body |
| `NOTIFY_SWAP_REJECTED_SUBJECT_TXT_PATH` | swap.rejected subject |
| `NOTIFY_SWAP_CANCELLED_TXT_PATH` | swap.cancelled body |
| `NOTIFY_SWAP_CANCELLED_SUBJECT_TXT_PATH` | swap.cancelled subject |
| `NOTIFY_WFH_STATE_CHANGED_TXT_PATH` | wfh.state_changed body |
| `NOTIFY_WFH_STATE_CHANGED_SUBJECT_TXT_PATH` | wfh.state_changed subject |
| `NOTIFY_COVER_ASSIGNED_TXT_PATH` | cover.assigned body |
| `NOTIFY_COVER_ASSIGNED_SUBJECT_TXT_PATH` | cover.assigned subject |
| `NOTIFY_SWAP_REQUESTED_TXT_PATH` | swap.requested body (used by both HAT and WFH swap paths) |
| `NOTIFY_SWAP_REQUESTED_SUBJECT_TXT_PATH` | swap.requested subject |
| `NOTIFY_SWAP_ACCEPTED_TXT_PATH` | swap.accepted body |
| `NOTIFY_SWAP_ACCEPTED_SUBJECT_TXT_PATH` | swap.accepted subject |
| `NOTIFY_SWAP_REJECTED_TXT_PATH` | swap.rejected body |
| `NOTIFY_SWAP_REJECTED_SUBJECT_TXT_PATH` | swap.rejected subject |
| `NOTIFY_SWAP_CANCELLED_TXT_PATH` | swap.cancelled body |
| `NOTIFY_SWAP_CANCELLED_SUBJECT_TXT_PATH` | swap.cancelled subject |

Templates have access to a single data struct with the relevant
fields per event. See `internal/notify/templates/*.tmpl` for the
bundled defaults.

## Operations

### Inspecting the outbox

The `notification_outbox` table is the source of truth for what's
queued and what's been delivered:

```sql
-- Pending rows (waiting to be sent or retried)
SELECT id, event_kind, channel, recipient, subject,
       attempts, last_error, next_attempt_at
FROM notification_outbox
WHERE status = 'pending'
ORDER BY next_attempt_at;

-- Recent failures
SELECT id, event_kind, channel, recipient, attempts, last_error
FROM notification_outbox
WHERE status = 'dead'
ORDER BY updated_at DESC
LIMIT 50;

-- Delivery success rate over the last 24h
SELECT status, COUNT(*) AS n
FROM notification_outbox
WHERE created_at > datetime('now', '-1 day')
GROUP BY status;
```

### Recovering from a dead-letter row

Dead rows are kept for inspection. To retry one manually:

```sql
UPDATE notification_outbox
SET status = 'pending',
    attempts = 0,
    last_error = NULL,
    next_attempt_at = datetime('now')
WHERE id = '<row-id>';
```

The worker will pick it up on the next tick.

## Adding a new channel

The design is intentionally channel-agnostic. To add Slack, for
example:

1. Add the channel to `internal/notify/channels/<name>/` with a type
   that implements `channels.Channel` (one method: `Send(ctx, msg)`).
2. Add a constructor that takes the channel-specific config.
3. Add a name constant to `internal/notify/doc.go`
   (`ChannelSlack = "slack"`).
4. Register the channel in `buildNotifier` in
   `internal/api/server.go`, gated by a new env var (e.g.
   `NOTIFY_SLACK_ENABLED`).
5. Add an `outbox.channel` value to the schema if it's a new string
   (it's already `TEXT` with no CHECK constraint, so no migration is
   required).
6. Update `Config.Validate` to accept the new channel name.
7. Tests follow the pattern in `internal/notify/channels/email/`.

No producer code changes; producer code only knows the
`notify.Notifier` interface, which is channel-agnostic.

## Code map

| Concern | Location |
|---|---|
| Public `Notifier` interface | `internal/notify/notify.go` |
| Event payloads | `internal/notify/events.go` |
| Config + env loading | `internal/notify/config.go` |
| `text/template` renderer | `internal/notify/renderer.go` |
| Bundled templates | `internal/notify/templates/*.tmpl` |
| Production notifier (outbox writer) | `internal/notify/channel_notifier.go` |
| Outbox worker (drain + retry) | `internal/notify/outbox_worker.go` |
| Email channel | `internal/notify/channels/email/email.go` |
| Log channel (dev fallback) | `internal/notify/channels/log/log.go` |
| Unsubscribe token utility | `internal/notify/token.go` |
| Per-member preferences | `internal/database/notification_preferences.go` |
| One-click unsubscribe web handler | `internal/web/unsubscribe_handler.go` |
| Outbox schema | `migrations/000013_add_notification_outbox.{up,down}.sql` |
| Per-row unsubscribe URL column | `migrations/000015_add_outbox_unsubscribe_url.{up,down}.sql` |
| Per-member preferences schema | `migrations/000014_add_notification_preferences.{up,down}.sql` |
| Server wiring | `internal/api/server.go` (`buildNotifier`) |
