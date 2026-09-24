# Tiki

A Go CLI and shared HTTP server for tracking work. Items have a sortable `float64` priority, multiple assignees, and generic tags. The architecture and roadmap are in [PLAN.md](PLAN.md).

## Run

Requires the Go version declared in `go.mod`. SQLite is embedded. Build the binary, initialize a fresh database, and start the server:

```sh
go build -o tiki .
./tiki init --db ./dev.db --name Colin --email colin@example.com
./tiki serve --db ./dev.db
```

`init` asks for a password and confirmation without displaying what you type. Passwords must contain at least 8 characters and at most 1024 bytes. It creates the first administrator and prints the user, never the password. Use the same `--db` path for `init` and `serve`; the server prints its absolute path on startup. Existing version-2 databases remain compatible; initialization is transactional and unsupported schema versions are rejected.

In another terminal, sign in and start working:

```sh
./tiki auth login --email colin@example.com
./tiki auth status
./tiki item create --title 'Keep search focused' --type bug --tag frontend
./tiki item list
```

Login asks for your password and saves the session automatically. No token copying or shell configuration is required. From source, replace `./tiki` with `go run .`. If your sandbox hides Git metadata, build with `-buildvcs=false`.

The default server is `http://127.0.0.1:8080`. Login to another server with `--server https://tiki.example.com`; subsequent commands remember that address. Explicit `--server` or `TIKI_SERVER` takes precedence over the saved address. HTTPS is required for remote servers; HTTP is allowed on loopback for development.

Administrators can provision additional users with an initial password:

```sh
./tiki user create --name Alex --email alex@example.com
./tiki item create --title 'Keyboard navigation' --type feature \
  --tag repo/tiki --assignee 1 --assignee 2
./tiki item move 2 --before 1
./tiki item update 1 --add-assignee 2 --status in_progress
./tiki item get 1 --json
./tiki item activity 1 --json
```

Use the IDs returned by commands; the example IDs assume a fresh workspace. New users sign in with their own email and password. `auth password` changes the current user's password, revokes all their sessions, and requires a new login. `auth logout` revokes the current session and removes the saved login.

## Authentication

Passwords are stored as salted Argon2id hashes. Successful login creates a random session valid for seven days; the server stores only its hash. The CLI keeps one active login in `tiki/session.json` under the OS user configuration directory (`$XDG_CONFIG_HOME` or `~/.config` on Linux), with file permissions `0600`. It never stores your password. The session is bound to its server address and is not sent through redirects or to a different server.

For scripts and agents, `init`, `auth login`, and `user create` accept `--password-stdin`. Feed the password from your secret manager or a protected file, for example:

```sh
./tiki auth login --email agent@example.com --password-stdin --json < /secure/path/password
```

One trailing line ending is removed; spaces within the password are preserved. `--json` never prompts or prints session secrets. Password changes are interactive in the CLI; automation can use the password-change API. There is no public sign-up or password-recovery endpoint; administrators create users.

Login and password-change requests allow ten attempts per minute per connecting IP, with at most two concurrent password-hashing operations. Forwarded IP headers are not trusted; when behind a reverse proxy, its connecting address shares this limit. Configure HTTPS at that proxy for team access. Session authentication applies to every API route except health, readiness, and login.

## Current behavior

- Items support `bug`, `feature`, and `task`. Statuses are `backlog`, `todo`, `in_progress`, `code_review`, `blocked`, `complete`, and `void`.
- Priority is a finite `float64`, ordered ascending with ID as the tie-breaker. Creation appends by default. Use `item move ID --before ID` or `--after ID` for positioning; scripts can also specify `--priority`. There are no discrete priority levels.
- Ordering is workspace-wide. Tag/status filters show a subset of that order; moving relative to an item uses its neighbor in the full workspace. Repeated midpoint insertion eventually exhausts float precision, so the server atomically renumbers priorities while preserving order. That increments item versions and expires existing pagination cursors.
- Assignment is a set: repeated `--assignee` flags on creation, then `--add-assignee`/`--remove-assignee`. Adding an existing member does not duplicate it. `item list --assignee ID` tests membership; `--assignee none` finds an empty set.
- Tags replace separate project and label entities. Names are trimmed/lowercased and created on first use. Repeated `--tag` filters use AND. Use `--add-tag` and `--remove-tag` to edit membership, and `tag list` to discover names. Tags are not access-control boundaries.
- API edits and moves require `version`. CLI `--if-version N` sends a version already read by the caller; otherwise the CLI fetches it immediately before submission. Agents editing previously read content should always pass that version. A stale edit exits with code 4 and reports the current version.
- Lists contain metadata, tags, and assignees; `get` includes the description. Item lists default to 50 rows, cap at 200, and return `next_cursor`; pass it as `--cursor`. Users/tags/activity use `next_after` and `--after`. Pagination is live and may shift when items move. Each item permits up to 100 assignees and 100 tags.
- Description input supports `--description` or `--body-file FILE` (`-` reads stdin), with a 256-KiB limit. Tag names allow up to 64 characters and titles up to 300.
- Admins provision users with email/password credentials. Members can edit all items, and viewers can only read. All roles currently share workspace visibility. Logout, expiry, and password changes invalidate sessions on subsequent requests.

There are no automatic write retries or idempotency keys yet. If a create times out after reaching the server, inspect recent items before repeating it.

## HTTP API

Call `POST /api/v1/auth/login` with `email` and `password`; use the returned `session_token` as `Authorization: Bearer <session_token>`. The CLI handles this automatically. The login response also contains `user` and `expires_at` (Unix seconds). JSON IDs are decimal strings so JavaScript clients preserve integer precision. Priorities are JSON numbers. Errors use `{"error":{"code":"...","message":"..."}}`, with `current_version` on stale-edit conflicts.

| Method | Path | Purpose |
| --- | --- | --- |
| POST | `/api/v1/auth/login` | Sign in with email/password. |
| GET | `/api/v1/auth/me` | Get the signed-in user. |
| POST | `/api/v1/auth/logout` | Revoke the current session. |
| POST | `/api/v1/auth/password` | Change password using `current_password` and `new_password`; revoke all user sessions. |
| POST / GET | `/api/v1/users` | Create a user (name, email, password, optional role) / list users. |
| POST / GET | `/api/v1/items` | Create / list items. |
| GET / PATCH | `/api/v1/items/{id}` | Read / edit an item. |
| POST | `/api/v1/items/{id}/move` | Move before/after another item. |
| GET | `/api/v1/items/{id}/activity` | Read durable activity. |
| GET | `/api/v1/tags` | List tags. |

Create body:

```json
{"title":"Fix keyboard focus","type":"bug","tags":["repo/tiki","frontend"],"assignees":["1","2"]}
```

Edit and move bodies:

```json
{"version":1,"add_assignees":["3"],"remove_tags":["frontend"],"status":"code_review"}
```

```json
{"version":2,"before":"42"}
```

The item list accepts `tag`, `status`, `assignee` (ID or `none`), `limit`, and `cursor` query parameters. Repeat `tag` to require multiple tags. Users and activity accept an ID in `after`; tags accept a name. Each accepts `limit` up to 200. Activity pages also cap stored event payloads at approximately 2 MiB, so use `next_after` rather than assuming a full page. Successful operations return HTTP 200. Send `Content-Type: application/json` for JSON bodies; other explicit media types return 415. Bodies are limited to 2 MiB (413 on overflow). Unknown API routes return JSON 404 errors; unsupported methods return JSON 405 errors with an `Allow` header. Request deadlines return 504. `/healthz` reports process liveness; `/readyz` checks database connectivity and returns 503 when unavailable.

CLI exit codes: 0 success, 1 unexpected failure, 2 validation/flag errors, 3 not found, 4 conflict/expired cursor, 5 authentication/authorization, 6 transport failure, timeout, unavailable service, or rate limit.

## Development

```sh
make check          # formatting, race tests, vet, binary build
make bench          # allocation and timing benchmarks at 10k and 100k items
```

The same checks run on pushes and pull requests through GitHub Actions. No extra Go tooling is required. Without Make, run `go test -race ./...`, `go vet ./...`, and `go build .`; use `gofmt -l main.go internal` to check formatting. In environments with hidden Git metadata, set `GOFLAGS=-buildvcs=false`.

Tests use temporary SQLite databases and loopback HTTP servers. They cover permissions, session lifecycle, membership rollback, persistence, pagination, concurrent edits through independent stores, read/write overlap, snapshot isolation, per-connection read-only settings, float rebalancing, bounded requests/responses, CLI stdin and exit codes, and redirect protection. To fuzz wire IDs:

```sh
go test ./internal/tiki -run '^$' -fuzz FuzzID -fuzztime 10s -parallel 2
```

Measured results and reproduction details are in [docs/performance.md](docs/performance.md). These local benchmarks do not establish the production workload targets in the plan.

## Code organization

| Package | Responsibility |
| --- | --- |
| `internal/tiki` | Domain types and limits, input validation, accounts/sessions, explicit SQL, ordering, pagination, and transactional activity. No HTTP or CLI dependencies. |
| `internal/httpapi` | Versioned routes, authentication and role checks, request limits, JSON errors, and health/readiness probes. |
| `internal/cli` | Cobra commands, server startup/shutdown, HTTP requests, saved credentials, and terminal/JSON output. |
| `main.go` | Process entry point. |

Keep business rules in the store and transport rules in the adapters. The store is a trusted in-process API: HTTP authorization belongs in `httpapi`, and all remote clients, including the future web UI, must use that boundary. Concrete types and ordinary functions are sufficient; there is no repository interface, ORM, dependency injection container, or generated layer.

SQLite uses WAL and `synchronous=FULL`. One connection serializes immediate write transactions, while a separate read-only pool has between two and eight connections, bounded by `GOMAXPROCS`. Every connection enables foreign keys and a five-second busy timeout. Item lists read ordering generation and rows in one deferred snapshot, so rebalancing cannot mix generations within a page. Reads do not acquire the writer lock. Mutations check versions and write activity inside the same transaction.

The database schema remains version 2. For a future schema change, add an explicit transactional migration in `Open`, increment `user_version`, and test opening a populated older database. Never recreate a populated database to upgrade it. Shutdown stops HTTP requests before closing the pools. Use local storage, and use a SQLite-consistent backup mechanism rather than copying a running database's main file.

Comments, full-text search, SSE, bulk operations, backup/export commands, and the web UI remain roadmap work. No parent ID or nesting is implemented.
