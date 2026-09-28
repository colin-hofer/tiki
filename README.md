# Tiki

Work tracking for humans and their suspiciously confident coding agents.

A live web board, CLI, and HTTP API for bugs, features, and tasks. Tags, multiple assignees, comments, and activity history included. Go + Svelte + SQLite, shipped as one binary. No certification in moving rectangles required.

## Run it

You'll need Go (see [go.mod](go.mod)), Node.js 22.12+ with npm, and Make to build from source:

```sh
make build
./tiki init --db ./tiki.db --name 'Your Name' --email you@example.com
./tiki serve --db ./tiki.db
```

`init` creates the first administrator and prompts for a password; run it once. Open **http://127.0.0.1:8080** and sign in. The built binary includes the UI and CLI downloads; Node can clock out now.

Admins invite teammates through **Manage people**. Members can edit tickets; viewers can read them. Everyone shares the same workspace—tags organize work, not permissions.

## Put it to work

In another terminal:

```sh
./tiki auth login --email you@example.com
./tiki item create --title 'Fix the thing we called a feature' --type bug --tag backend --assignee me
./tiki item list --assignee me
./tiki item list --query 'thing'
./tiki item get 123 --json
./tiki item update 123 --if-version 7 --status in_progress
./tiki item comment 123 --body 'Found it. Regrettably, it was my code.'
```

Replace `123` and `7` with the ticket ID and version you read. Use `--help` on any command, `--json` for scripts, and `--if-version` when editing previously read tickets to catch conflicting changes.

Prefer the browser? Press **?** for shortcuts or **Ctrl/Cmd+K** for the command menu.

## Take the CLI with you

Open **CLI & agents** in your workspace for installation commands, or replace the example URL below with your server:

```sh
curl -fsS 'https://tiki.example.com/api/v1/cli/install.sh' | sh -s -- 'https://tiki.example.com'
tiki auth login --email you@example.com
```

The installer supports Linux and macOS on x86-64/ARM64, installs to `~/.local/bin`, and remembers your server. Follow its PATH instructions if needed. Run `tiki update` to match the CLI bundled with that server. Remote servers require HTTPS.

## Bring your agent

Give it the manual. It has enough confidence already.

```sh
tiki skill install            # install to ~/.agents/skills/tiki
# Or share the skill with this repository's contributors:
tiki skill install --project  # install to .agents/skills/tiki
```

Use `--dir ~/.claude/skills` for Claude Code. For agents without skill support, add: **“For Tiki ticket work, run `tiki skill` first.”** The CLI carries the [instructions](skills/instructions.md), so updating it keeps the manual current.

## Hack on it

```sh
make dev     # UI + API with automatic reloads
make check   # formatting, lint, builds, Go race tests, browser tests, vet
make format  # apply formatting
```

Development runs at **http://127.0.0.1:5173**. The first run asks you to set a password for **dev@tiki.local**. Data persists in `.dev/tiki.db`, separate from your regular database.

See the [frontend guide](frontend/README.md) for overrides and browser-test setup. Backend-only checks: `go test -tags dev -race ./...`.

## Ship it

For a Linux systemd host with SSH access as root (or passwordless sudo):

```sh
./scripts/deploy.sh root@your-server
```

Put an HTTPS reverse proxy or Cloudflare Tunnel in front of `127.0.0.1:8080`. The [deployment scripts](scripts/install-server.sh) check host dependencies, install the service, back up existing data, and check readiness on updates. A fresh install prints the administrator setup command. Rerun to deploy changes.
