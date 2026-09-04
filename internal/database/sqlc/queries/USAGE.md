# Generated queries — caller pattern

SQLC emits one Go method per `-- name: <Query>` line. The 138
generated methods in `internal/database/sqlc/*.sql.go` all have
non-zero callers, but the counting is subtle because callers take
**two** forms:

## Form A: wrapper methods in `internal/database`

The `database.DB` struct exposes hand-written wrapper methods like
`AddTeamMember(name, email)` that translate arguments into a typed
Params struct and call `db.queries.AddTeamMember(ctx, params)`.
This is the path the WFH service, the rota engine, and most tests
take.

## Form B: `db.GetQueries().X(...)`

API handlers, auth middleware, and certain web handlers reach the
SQLC layer **directly** via `db.GetQueries().QueryName(...)` when
no wrapper exists. See `internal/api/server.go`,
`internal/auth/session.go`, `internal/web/team_handlers.go` for
examples.

## Dead-code analyser caveat

A static `dead_code` query against this package returns ~20 hits.
They are **false positives** — the analyser cannot trace
`db.GetQueries().X(...)` calls through the cross-package
indirection that Form B uses, so it sees the SQLC method as
unreachable. To verify a candidate is truly dead, search across
the entire repository:

```sh
grep -r "\b<QueryName>\b" --include='*.go' internal/ \
  | grep -v 'internal/database/sqlc/'
```

If the only hits are within `internal/database/sqlc/` itself
(definition), the method is genuinely dead. Otherwise, it has
a wrapper caller or a direct-`GetQueries()` caller that the
graph missed.

## SQLC exclusion in v1.28

SQLC v1.28 has **no `exclude` option** for individual queries.
The two structural workarounds are:

  1. **Split `.sql` files into multiple packages** — declare
     multiple `sql:` blocks in `sqlc.yaml`, each pointing at a
     subdirectory of `queries/`. SQLC then emits one Go package
     per block. This is the precursor to the
     per-aggregate-repository refactor (see CONSOLIDATED_REFERENCE).

  2. **Move dead `.sql` files to `queries/_deprecated/` and
     exclude that path** — keeps the SQL for documentation but
     stops generating Go. Use only when wrappers + Form B
     callers are both truly absent for every query in the file.

Until a query file is confirmed dead via the grep above, leave
it in the main `queries/` tree.
