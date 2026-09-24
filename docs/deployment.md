# Deploying Tiki

Tiki runs as one binary under systemd, listening on `127.0.0.1:8080`. The
Cloudflare Tunnel on the same host forwards the public hostname to that address.
The UI, API, live updates, and CLI downloads all use the same hostname.

## One-time setup

The server needs Linux (amd64 or arm64), systemd, SSH, and these utilities:

```sh
# On the Debian/Ubuntu server, as root:
apt-get update
apt-get install -y --no-install-recommends sqlite3 curl util-linux iproute2
```

The local build machine needs Go from `go.mod`, Node.js 22.12+ with npm, Make,
Bash, SSH, tar, and a SHA-256 utility. `make check` also uses Python 3 for the
deployment regression checks. An SSH alias or Tailscale hostname works as the target.
The SSH user must be root or have passwordless sudo for installation. Keep SSH
host-key verification enabled. The script opens one private SSH control connection:
enter your key passphrase once, or use an already-unlocked SSH agent. Every later
command reuses that connection, and it is closed on exit. A lost connection fails
the deployment instead of prompting repeatedly. No SSH configuration changes or
passphrase storage are needed.

From the repository root on your build machine:

```sh
./scripts/deploy.sh root@198.199.122.239
```

This builds the frontend and CLI downloads in parallel, cross-compiles a stripped
static Linux server binary, and uploads one compressed archive. No Go, Node.js, source checkout, or container
runtime is needed on the server. The script deploys the current working tree,
including uncommitted changes. Run `make check` before releasing changes.

Unchanged frontend inputs and outputs reuse the last successful build. Go's own
build cache handles CLI compilation, and unchanged CLI archives are reused after
checksum verification. The cache lives in `.dev/build/` and
`.dev/frontend-build.json`; deleting those paths forces the corresponding work
again without touching the development database. Changed/deleted source files,
frontend environment files, Node versions, and `VITE_*` variables invalidate the
frontend cache.

On a fresh installation, the service is installed and enabled but remains stopped
until an administrator exists. Initialize it once over an interactive SSH session:

```sh
ssh -t root@198.199.122.239 'runuser -u tiki -- /opt/tiki/current/tiki init --db /var/lib/tiki/tiki.db --name Colin --email colin.hofer@accurise.com'
ssh root@198.199.122.239 'systemctl start tiki && curl -fsS http://127.0.0.1:8080/readyz'
```

`init` prompts for a password without echoing it. Do not put passwords in command
arguments. If you use a non-root SSH account, use `sudo -u tiki` for initialization
and `sudo systemctl start tiki`. Then sign in at the public HTTPS hostname.

The tunnel does not need to change. The service runs as the unprivileged `tiki`
user, starts on boot, and restarts on failure. Its filesystem is read-only except
for its private temporary directory and `/var/lib/tiki`. The public ports 80,
443, and 8080 need not be opened for Tiki. This installer does not change the
firewall, SSH, Tailscale, or Cloudflare configuration.

## Updates

Run the same deployment command. If the binary and service are unchanged, the
binary is not uploaded. A healthy matching installation is left running, while
retention cleanup still runs. That creates no release, backup, or interruption.
The manifest is checked again under the server-side deployment lock before any
installation, including when the current binary is reused.

For a changed installation, the installer stops Tiki, creates and integrity-checks a
SQLite backup, switches the release symlink, starts Tiki (which runs migrations),
and checks `/readyz`. Expect a brief interruption; browsers reconnect their live
event streams automatically.

| Server path | Purpose |
| --- | --- |
| `/opt/tiki/releases/` | Versioned binaries and their systemd units |
| `/opt/tiki/current` | Active release |
| `/opt/tiki/previous` | Previous release, retained for recovery |
| `/var/lib/tiki/tiki.db` | Persistent application database |
| `/var/backups/tiki/` | Private SQLite snapshots from deployments |
| `/etc/systemd/system/tiki.service` | Installed systemd unit |

Useful commands on the server:

```sh
systemctl status tiki
journalctl -u tiki -n 100 --no-pager
curl -fsS http://127.0.0.1:8080/readyz
```

The deployment script can also run in CI once the runner has SSH access. No
automatic deployment workflow is enabled by these files.

## Backups and recovery

Deployments take a consistent backup while Tiki is stopped, before any migration.
After a successful deployment or an unchanged healthy check, cleanup keeps:

- The active and previous releases. Other automatically named releases are removed.
- The seven newest deployment backups, plus snapshots associated with the active
  and previous releases if those are older.
- All manually named backups and directories. The live database is never pruned.

Failed deployments do not prune recovery files. Temporary uploads and the local
SSH control socket are removed on exit, including handled interruptions.
These local snapshots do not protect against losing the droplet. Copy backups to
off-host storage, and schedule regular backups if data changes between deployments.
For a manual consistent snapshot on the server:

```sh
umask 077
sqlite3 /var/lib/tiki/tiki.db ".backup '/var/backups/tiki/manual-$(date -u +%Y%m%dT%H%M%SZ).db'"
```

Never copy only the main file of a running WAL database. SQLite's `.backup` command
includes committed data from the WAL.

If installation fails before activating the new release, the installer attempts
to restart the previously running service. If activation or readiness fails, it
stops Tiki and prints the previous release and snapshot paths. It does **not**
automatically restore an older database, which could discard newer writes.

Inspect the journal first. A code-only rollback can switch `current` back to
`previous`, reinstall that release's `tiki.service`, run `systemctl daemon-reload`,
and restart Tiki **only when that binary supports the current database version**.
Inspect the version with `sqlite3 /var/lib/tiki/tiki.db 'PRAGMA user_version;'`.

For an incompatible schema, recovery requires the matching pre-deployment backup:
stop Tiki, take a snapshot of the failed database for investigation, restore the
chosen backup with owner `tiki:tiki` and mode `0600`, remove the stopped database's
old `-wal` and `-shm` sidecars, restore the matching release and service unit, and
restart. Restoring a snapshot discards writes made after that snapshot. Test
restoration on a separate database before depending on a backup.

## Cloudflare behavior

Use the public HTTPS hostname when configuring the CLI and sharing invitations.
Tiki supplies `text/event-stream` and 15-second heartbeats for live updates. Verify
live updates in two browser sessions through the public hostname after setup.
Keep API responses uncached; the app already sends `Cache-Control: no-store`.

Cloudflare Access authentication is separate from Tunnel and requires additional
CLI/installer integration. Tiki currently uses its own login and invitation system.
It does not trust forwarded IP headers: users behind `cloudflared` share the
authentication limit of ten attempts per minute. Trusted-proxy support is separate
work if that limit becomes restrictive.
