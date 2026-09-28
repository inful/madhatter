# Documentation Index

A navigable map of every Markdown file in this repository, with one-line descriptions and the audience each one is written for. Use this when you don't know where to look; AGENTS.md's *Documentation Triggers* table is what to read when you DO know which file you need to update.

Status legend: **Current** — verified against the code on 2026-09-28 · **Historical** — kept for context but no longer authoritative · **Live** — kept current with shipped drift.

## Where to look

| If you want to… | Read |
| --- | --- |
| Run the app, configure OAuth, find every env var | [README.md](README.md) |
| Understand a security contract or write a handler | [AGENTS.md](AGENTS.md) — *Security Guarantees* and *Documentation Triggers* |
| Set up Forgejo or GitLab OAuth | [AUTH_SETUP.md](AUTH_SETUP.md) |
| Generate or use an API token | [API_AUTH_IMPLEMENTATION.md](API_AUTH_IMPLEMENTATION.md) |
| Look up an API endpoint, method, or auth model | [`GET /docs`](README.md#api-reference) on the running server (HUMA-generated), or [CONSOLIDATED_REFERENCE.md § API Endpoints](CONSOLIDATED_REFERENCE.md#api-endpoints) |
| Look up a web route | [`internal/web/routes.go`](internal/web/routes.go) — the source of truth |
| Understand the holiday subsystem | [HOLIDAY_IMPLEMENTATION.md](HOLIDAY_IMPLEMENTATION.md) |
| Understand the notification pipeline | [docs/NOTIFICATIONS.md](docs/NOTIFICATIONS.md) |
| Understand the seat-cap / Assigned WFH picker | [docs/ASSIGNED_WFH.md](docs/ASSIGNED_WFH.md) |
| Find a SQLC query and the right caller pattern | [internal/database/sqlc/queries/USAGE.md](internal/database/sqlc/queries/USAGE.md) |
| Add a WFH feature | [plans/assigned-wfh-plan.md](plans/assigned-wfh-plan.md) — the model for spec-with-shipped-drift docs |

## The files

| File | Audience | Lines | Last touched | Status |
| --- | --- | --- | --- | --- |
| [README.md](README.md) | Operators + new contributors | 827 | 2026-09-28 | Current |
| [AGENTS.md](AGENTS.md) | Developers (humans + AI) | 695 | 2026-09-28 | Current |
| [CONSOLIDATED_REFERENCE.md](CONSOLIDATED_REFERENCE.md) | Mixed | 525 | 2026-09-28 | Current (focused reference) |
| [AUTH_SETUP.md](AUTH_SETUP.md) | Operators | 378 | 2026-09-28 | Current |
| [API_AUTH_IMPLEMENTATION.md](API_AUTH_IMPLEMENTATION.md) | API users + reviewers | 386 | 2026-09-28 | Current |
| [docs/NOTIFICATIONS.md](docs/NOTIFICATIONS.md) | Operators + devs | 257 | 2026-09-28 | Current |
| [docs/ASSIGNED_WFH.md](docs/ASSIGNED_WFH.md) | Users + admins | 175 | 2026-09-04 | Current |
| [plans/assigned-wfh-plan.md](plans/assigned-wfh-plan.md) | Mixed | 700 | 2026-09-03 | Live (status-tracked plan) |
| [internal/database/sqlc/queries/USAGE.md](internal/database/sqlc/queries/USAGE.md) | Developers | 60 | 2025-09-05 | Current |
| [HOLIDAY_IMPLEMENTATION.md](HOLIDAY_IMPLEMENTATION.md) | Developers + ops | 278 | 2026-09-28 | Current (see "Notes" below) |
| [IMPLEMENTATION_PLAN.md](IMPLEMENTATION_PLAN.md) | — | 349 | 2026-09-28 | **Historical** (all phases shipped; engine API renamed) |
| [SQLC_MIGRATION_GUIDE.md](SQLC_MIGRATION_GUIDE.md) | — | 387 | 2026-09-28 | **Historical** (migration complete; superseded by USAGE.md) |
| [plans/oauth2-auth-spec.md](plans/oauth2-auth-spec.md) | — | 299 | 2026-09-28 | **Historical** (env vars renamed: per-provider FORGEJO_*/GITLAB_*) |

## Documenting for this repo

The *Documentation Triggers* table in [AGENTS.md](AGENTS.md#documentation-triggers) is the contract every change follows: new env var → README env-var table; new API endpoint → README API Reference + `GET /docs`; new CLI subcommand → README CLI Commands + CONSOLIDATED_REFERENCE.md; in-app help copy → `internal/web/templates/help.html`. Updates ship in the same commit as the code change — follow-up doc PRs are a smell.

If you write a long feature plan, follow the [plans/assigned-wfh-plan.md](plans/assigned-wfh-plan.md) model: a *Locked Decisions* table at the top, a status tracker that flips each row as the corresponding phase lands, and drift notes when the shipped implementation diverges from the original spec. Plans that age into that state are the only ones worth keeping on disk.

## Notes

The four *Historical* docs at the bottom of the table (HOLIDAY_IMPLEMENTATION, IMPLEMENTATION_PLAN, SQLC_MIGRATION_GUIDE, plans/oauth2-auth-spec) still carry useful narrative — the holiday scheduler architecture, the WFH feature design, the OAuth flow, the original SQLC phasing — but they are not authoritative. Cross-check against the code before quoting from one in a PR or a Slack thread; the *Current* docs above are the ones to link from new code.

Only one of the *Current* docs (HOLIDAY_IMPLEMENTATION.md) still ships a few pieces of stale advice the September 2026 review caught — the env-var table mentions `HOLIDAY_FETCH_INTERVAL` and `HOLIDAY_LOOKAHEAD` as configurable, with explicit "is not read from env" disclaimers next to them so a reader who skims the table won't be misled. The next major version tag should remove those disclaimers entirely once the doc is rewritten.
