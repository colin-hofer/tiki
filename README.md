# Tiki

A Go CLI and shared HTTP server for tracking work. Items have a sortable `float64` priority, multiple assignees, and generic tags. The architecture and roadmap are in [PLAN.md](PLAN.md).

## Local development

Install Go (the version in `go.mod`), Node.js 22.12+ with npm, and Make. From the repository root, run:

```sh
make dev
```

Open **http://127.0.0.1:5173**. The first run installs the locked frontend dependencies, builds the Go API, and asks you to choose a password for **dev@tiki.local**. Sign in with that account. Later runs reuse the database and account without prompting.

Frontend edits hot-reload. Go and SQL edits rebuild and restart the API automatically; a compilation error leaves the previous API running until you fix it. Ctrl+C stops both servers. Development data persists in `.dev/tiki.db`, separate from the default `tiki.db`; nothing resets your database on startup. The dev API listens only on `127.0.0.1:8081`.

Optional overrides:

```sh
TIKI_DEV_API_PORT=18081 make dev
TIKI_DEV_DB=/absolute/path/to/dev.db make dev
TIKI_API_URL=http://127.0.0.1:8080 make dev  # use an already-running API
```

To use the CLI against the development server, run `.dev/tiki-api auth login --server http://127.0.0.1:5173 --email dev@tiki.local`. Vite forwards API requests to Go, and invite links use the same browser address. A saved CLI login remembers that address.

## Build and run the complete app

```sh
make build
# For a fresh database, create your first administrator once:
./tiki init --db ./tiki.db --name Colin --email colin@example.com
./tiki serve --db ./tiki.db
```

Open **http://127.0.0.1:8080**. `make build` installs frontend dependencies when needed, checks Svelte/TypeScript, builds the frontend and CLI downloads, and embeds them into the Go binary. The resulting `tiki` serves the UI and API together and needs neither Node nor a `frontend/dist` directory at runtime. Plain `go build` embeds the existing frontend and CLI `dist` outputs; use `make build` to ensure it is current.

`init` asks for a password and confirmation without displaying what you type. Passwords must contain at least 8 characters and at most 1024 bytes. It creates the first administrator and prints the user, never the password. Use the same `--db` path for `init` and `serve`; the server prints its absolute path on startup. New databases initialize transactionally from `internal/tiki/schema.sql`. During this pre-release stage, schema changes require a fresh database; older schema versions are rejected.

In another terminal, sign in and start working:

```sh
./tiki auth login --email colin@example.com
./tiki auth status
./tiki item create --title 'Keep search focused' --type bug --tag frontend
./tiki item list
```

Login asks for your password and saves the session automatically. No token copying or shell configuration is required. From source after `make frontend cli`, replace `./tiki` with `go run .`. If your sandbox hides Git metadata, build with `-buildvcs=false`.

The default server is `http://127.0.0.1:8080`. Save your workspace address once, then omit it from every command:

```sh
./tiki config set-server https://tiki.example.com
./tiki auth login --email colin@example.com
./tiki item list
./tiki config show
```

Login with `--server URL` also saves that address. Selection order is `--server`, then `TIKI_SERVER`, then saved config, then the local default. The server setting lives in `tiki/config.json` under the OS configuration directory (`~/.config` on Linux) and survives logout and password changes. Existing session-only settings are still recognized. HTTPS is required for remote servers; HTTP is allowed on loopback for development.

## Install the CLI from your workspace

Open **Install CLI** in the web toolbar or command menu, then copy the install command into your terminal. Every role can use it. For example:

```sh
curl -fsS 'https://tiki.example.com/api/v1/cli/install.sh' | sh -s -- 'https://tiki.example.com'
tiki auth login --email you@example.com
```

The script detects Linux or macOS on x86-64/ARM64, downloads the matching CLI from your workspace, verifies its SHA-256 checksum, and installs it in `~/.local/bin` without sudo or Go. It saves the workspace URL automatically. If that directory is not on your PATH, the script prints the setup instructions. Sign in with your own password; copied commands contain no session token. Run the command again to upgrade. Failed downloads or checksum verification leave an existing CLI intact. `TIKI_INSTALL_DIR` can override the destination. The dialog includes a link to read the script before running it.

`make build` bundles all four compressed CLI downloads and their checksums into the server binary. Builds reuse Go's compiler cache; `make cli` refreshes downloads explicitly. `make dev` prepares the same downloads, which Vite serves through its existing API proxy. While the dev server is running, run `make cli` after CLI source changes to refresh downloads. The installer and downloads are public and served by Tiki itself; no release host or separate file server is required. HTTPS is required outside localhost.

## Invite your team

As an administrator, open **Manage people** from the web toolbar or command menu, then choose **Invite people**, or run:

```sh
./tiki invite create                          # member access, valid for 7 days
./tiki invite create --role viewer --expires-in 24h
./tiki invite list                            # active, unclaimed invites
./tiki invite revoke 1                        # ID returned when creating/listing
```

Share the generated link privately. The recipient opens it, chooses a name, email, and password, and lands on the board signed in. Each link creates exactly one account. Links expire after seven days by default; CLI lifetimes range from one minute to 30 days. Roles are `member` (default), `viewer`, or `admin`. Recipients cannot change the assigned role. Revoke unused links from the CLI or the dialog that created them.

Use the browser-facing workspace address when configuring the CLI: the link uses that address. A loopback link works only on the same computer; team invitations need your reachable HTTPS address. Invite tokens are kept in the URL fragment, out of HTTP URL/access logs, and only their hashes are stored in SQLite. Lists never reveal tokens; create another invite if a link is lost. `invite create --json` returns `id`, `url`, `role`, and `expires_at` for scripts. Other ordinary command output never prints session credentials.

After joining, teammates can run `tiki config set-server https://tiki.example.com` followed by `tiki auth login --email THEIR_EMAIL` to use the CLI with their chosen password.

## Manage people

The **People** dialog lists names, emails, and roles, with search, role controls, and removal. Admins can also use:

```sh
./tiki user list
./tiki user role 2 --role viewer
./tiki user remove 2
```

Role changes apply immediately to existing sessions and update open browsers. Removing a user signs them out and prevents further login or assignment; their identity, ticket history, and existing assignments remain available. **Show removed users** includes those identities in the People list. A new invite claimed with the same email restores access with a new password and the invite's role. Old sessions stay revoked. The last active administrator cannot be removed or demoted, including through concurrent requests. Demoting or removing an administrator also revokes their outstanding invitations.

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

One trailing line ending is removed; spaces within the password are preserved. `--json` never prompts or prints session secrets. Password changes are interactive in the CLI; automation can use the password-change API. Registration requires a valid administrator-created invite; there is no unrestricted sign-up or password-recovery endpoint. Administrators can also create accounts directly.

Login, password-change, invite inspection, and invite-claim requests share a limit of ten attempts per minute per connecting IP, with at most two concurrent password-hashing operations. Forwarded IP headers are not trusted; when behind a reverse proxy, its connecting address shares this limit. Configure HTTPS at that proxy for team access. Session authentication applies to every API route except health, readiness, login, public invite inspection/claim, and CLI installer/download routes.

## Current behavior

- Items support `bug`, `feature`, and `task`. Statuses are `backlog`, `todo`, `in_progress`, `code_review`, `blocked`, `complete`, and `void`.
- Priority is a finite `float64`, ordered ascending with ID as the tie-breaker. Creation appends by default. Use `item move ID --before ID` or `--after ID` for positioning; scripts can also specify `--priority`. There are no discrete priority levels.
- Ordering is workspace-wide. Tag/status filters show a subset of that order; moving relative to an item uses its neighbor in the full workspace. Repeated midpoint insertion eventually exhausts float precision, so the server atomically renumbers priorities while preserving order. That increments item versions and expires existing pagination cursors.
- Assignment is a set: repeated `--assignee` flags on creation, then `--add-assignee`/`--remove-assignee`. Adding an existing member does not duplicate it. `item list --assignee ID` tests membership; `--assignee none` finds an empty set.
- Tags replace separate project and label entities. Names are trimmed/lowercased and created on first use. Repeated `--tag` filters use AND. Use `--add-tag` and `--remove-tag` to edit membership, and `tag list` to discover names. Tags are not access-control boundaries.
- API edits and moves require `version`. CLI `--if-version N` sends a version already read by the caller; otherwise the CLI fetches it immediately before submission. Agents editing previously read content should always pass that version. A stale edit exits with code 4 and reports the current version.
- Lists contain metadata, tags, and assignees; `get` includes the description. Item lists default to 50 rows, cap at 200, and return `next_cursor`; pass it as `--cursor`. Users/tags/activity use `next_after` and `--after`. Pagination is live and may shift when items move. Each item permits up to 100 assignees and 100 tags.
- Description input supports `--description` or `--body-file FILE` (`-` reads stdin), with a 256-KiB limit. Tag names allow up to 64 characters and titles up to 300.
- Admins invite people to create their own accounts or provision users directly with email/password credentials. Members can edit all items, and viewers can only read. All roles currently share workspace visibility. Logout, expiry, and password changes invalidate sessions on subsequent requests.

There are no automatic write retries or idempotency keys yet. If a create times out after reaching the server, inspect recent items before repeating it.

## HTTP API

Call `POST /api/v1/auth/login` with `email` and `password`; use the returned `session_token` as `Authorization: Bearer <session_token>`. The CLI handles this automatically. The login response also contains `user` and `expires_at` (Unix seconds). JSON IDs are decimal strings so JavaScript clients preserve integer precision. Priorities are JSON numbers. Errors use `{"error":{"code":"...","message":"..."}}`, with `current_version` on stale-edit conflicts.

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/v1/cli` | Available CLI platforms. |
| GET | `/api/v1/cli/install.sh` | Public shell installer. |
| GET | `/api/v1/cli/downloads/{platform}.gz` | Public compressed CLI; `{platform}.sha256` contains its checksum. |
| POST | `/api/v1/auth/login` | Sign in with email/password. |
| GET | `/api/v1/auth/me` | Get the signed-in user. |
| POST | `/api/v1/auth/logout` | Revoke the current session. |
| POST | `/api/v1/auth/password` | Change password using `current_password` and `new_password`; revoke all user sessions. |
| POST / GET | `/api/v1/invites` | Admin: create (`role`, optional `expires_in` seconds, default 604800) / list active invites. |
| DELETE | `/api/v1/invites/{id}` | Admin: revoke an unused invite. |
| POST | `/api/v1/auth/invite` | Public: inspect a link using `{ "token": "..." }`; returns role and expiry. |
| POST | `/api/v1/auth/join` | Public: consume an invite with `token`, `name`, `email`, `password`; returns the same session shape as login. |
| POST / GET | `/api/v1/users` | Create a user (name, email, password, optional role) / list identities, including removed users with `removed_at` (Unix seconds). |
| PATCH / DELETE | `/api/v1/users/{id}` | Admin: set `role` / remove access and revoke sessions, preserving history. |
| GET | `/api/v1/board` | Initial board snapshot and paginated user/tag directories. |
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

`GET /api/v1/board` accepts the same `tag`, `status`, and `assignee` filters as the item list. Its `columns` object maps statuses to item pages from one SQLite read snapshot. Active statuses return up to 100 summaries each; backlog, complete, and void return up to 20 each. An explicit status filter returns up to 100 regardless of status. Summaries omit descriptions. Continue each column through `/items` using that column's `next_cursor` and the same filters. The board endpoint rejects `limit` and `cursor`; it also includes `users` and `tags` page objects (up to 200 entries each), whose `next_after` continues through the corresponding directory endpoint. This reduces a normal web session restore to three API requests: session, event stream, and board.

`GET /api/v1/events` is an authenticated SSE stream using the same bearer token as other endpoints. `ready` asks for an initial snapshot; `change` carries JSON `{ "items": [/* current tickets */], "users": true, "reset": true }` with unused fields omitted. Merge items by ID only when their version is newer. `users` invalidates the user directory, and `reset` requests a fresh snapshot. Subscribe before reading the snapshot to avoid missing concurrent edits. Every reconnect starts with `ready`; the stream does not require replay cursors. `expired` closes a revoked/expired session.

The stream caps concurrent connections at 256, limits each catch-up batch to 64 activity records and approximately 2 MiB of ticket JSON, and sends `reset` if those bounds or a global ordering reset require a new snapshot. Slow writes disconnect after five seconds. Heartbeats every 15 seconds also recheck credentials and catch changes made through another database connection/process. At a reverse proxy, disable response buffering and allow an idle timeout above 15 seconds (`X-Accel-Buffering: no` is already sent). Browser connections use header authentication through streaming fetch, keeping tokens out of URLs.

The item list accepts `tag`, `status`, `assignee` (ID or `none`), `limit`, and `cursor` query parameters. Repeat `tag` to require multiple tags. Users and activity accept an ID in `after`; tags accept a name. Each accepts `limit` up to 200. Activity pages also cap stored event payloads at approximately 2 MiB, so use `next_after` rather than assuming a full page. Successful operations return HTTP 200. Send `Content-Type: application/json` for JSON bodies; other explicit media types return 415. Bodies are limited to 2 MiB (413 on overflow). Unknown API routes return JSON 404 errors; unsupported methods return JSON 405 errors with an `Allow` header. Request deadlines return 504. `/healthz` reports process liveness; `/readyz` checks database connectivity and returns 503 when unavailable.

CLI exit codes: 0 success, 1 unexpected failure, 2 validation/flag errors, 3 not found, 4 conflict/expired cursor, 5 authentication/authorization, 6 transport failure, timeout, unavailable service, or rate limit.

## Development

```sh
make check          # formatting, frontend checks/build, race tests, vet, bundled binary
make bench          # allocation and timing benchmarks at 10k and 100k items
```

The same checks run on pushes and pull requests through GitHub Actions. For backend-only work without Node or generated assets, use `go test -tags dev -race ./...`. The `dev` build tag omits embedded UI assets; full integration checks use the real frontend build. In environments with hidden Git metadata, set `GOFLAGS=-buildvcs=false`. Browser checks run with `npm --prefix frontend test`; see [frontend/README.md](frontend/README.md) for Chromium setup.

Tests use temporary SQLite databases and loopback HTTP servers. They cover permissions, single-use and concurrent invite claims, role changes and removal, concurrent last-admin protection, persistent CLI server settings, session lifecycle, membership rollback, persistence, pagination, concurrent edits through independent stores, read/write overlap, snapshot isolation, per-connection read-only settings, float rebalancing, bounded requests/responses, CLI stdin and exit codes, and redirect protection. To fuzz wire IDs:

```sh
go test ./internal/tiki -run '^$' -fuzz FuzzID -fuzztime 10s -parallel 2
```

Measured results and reproduction details are in [docs/performance.md](docs/performance.md). These local benchmarks do not establish the production workload targets in the plan.

## Code organization

| Package | Responsibility |
| --- | --- |
| `internal/tiki` | Domain types and limits, input validation, accounts/sessions, explicit SQL, ordering, pagination, and transactional activity. No HTTP or CLI dependencies. |
| `internal/httpapi` | UI/API routing, authentication and role checks, request limits, JSON errors, and health/readiness probes. |
| `frontend` | Svelte/TypeScript UI, Vite development integration, and the Go handler for embedded assets. |
| `internal/cli` | Cobra commands, server startup/shutdown, HTTP requests, saved credentials, and terminal/JSON output. |
| `main.go` | Process entry point. |

Keep business rules in the store and transport rules in the adapters. The store is a trusted in-process API: HTTP authorization belongs in `httpapi`, and all remote clients, including the web UI, must use that boundary. Concrete types and ordinary functions are sufficient; there is no repository interface, ORM, dependency injection container, or generated layer.

SQLite uses WAL and `synchronous=FULL`. One connection serializes immediate write transactions, while a separate read-only pool has between two and eight connections, bounded by `GOMAXPROCS`. Every connection enables foreign keys and a five-second busy timeout. Item lists read ordering generation and rows in one deferred snapshot, so rebalancing cannot mix generations within a page. Reads do not acquire the writer lock. Mutations check versions and write activity inside the same transaction.

All tables, indexes, and triggers are defined in `internal/tiki/schema.sql` (currently schema version 4). There are no migrations or compatibility layers during this pre-release stage; use a fresh database when the schema changes. Shutdown stops HTTP requests before closing the pools. Use local storage, and use a SQLite-consistent backup mechanism rather than copying a running database's main file.

The web UI receives committed ticket updates over SSE and merges them without polling or refetching each ticket. Comments, full-text search, bulk operations, and backup/export commands remain roadmap work. No parent ID or nesting is implemented.
