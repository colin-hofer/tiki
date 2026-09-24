# Import the Accura tickets

The filtered CSV is `exports/tiki-syncify-incomplete-2026-09-24.csv`: 151 Syncify
tickets (113 tasks, 38 bugs). The 760 completed tickets and 23 `void` tickets are
excluded. The original full exports are unchanged.

The importer uses Python's standard library and the existing Tiki CLI. Run these
commands from the repository root, with the CLI already signed in to the target
server. Use `--tiki /path/to/tiki` if the binary is elsewhere.

```sh
# Local validation, without a server connection:
python3 scripts/import-accura.py exports/tiki-syncify-incomplete-2026-09-24.csv --validate-only

# Read-only preflight: resolve users and check saved progress against the server.
python3 scripts/import-accura.py exports/tiki-syncify-incomplete-2026-09-24.csv
```

Assignees match active Tiki users by an exact name, ignoring case and surrounding
whitespace. Missing or ambiguous matches are reported and omitted from assignments;
the tickets are still imported with any successfully matched assignees. If none
match, the ticket is unassigned. Optionally use `--assignee-map users.json` to
supply overrides:

```json
{
  "Accura user name": "3",
  "User to deliberately leave unassigned": null
}
```

Values are existing Tiki user ID strings. `null` explicitly omits that assignment.
The importer does not provision users. `./tiki user list --json --limit 200` shows
the target user directory; follow `next_after` with `--after` if necessary.

Take a server database snapshot using the backup command in
[deployment instructions](deployment.md#backups-and-recovery), then apply:

```sh
python3 scripts/import-accura.py exports/tiki-syncify-incomplete-2026-09-24.csv --apply
# Optionally add --assignee-map users.json to override automatic matching.
```

Each item gets its CSV title, combined description, type, status, numeric priority,
mapped assignees, and tags. The script also adds `syncify` and an Accura source link.
It reads back each created item and verifies its fields. Tiki assigns new item IDs,
the signed-in user as creator, and import-time timestamps. No Accura comments or
history are imported. The CSV's Accura priority values become Tiki ordering ranks.

Progress is saved beside the CSV in `*.import-state.json`, keyed by Accura UUID,
with the target server and a CSV checksum. Keep that file and rerun the same
command to resume. A local lock prevents two apply runs sharing the checkpoint.
Run this migration from one machine and keep the CSV unchanged while importing.

Before each create, the checkpoint records a `pending` UUID. If a command fails
after a request may have reached the server, the importer stops and refuses to
repeat that create. Inspect the target's `syncify` tickets and their Accura source
links. Once the outcome is known, reconcile the checkpoint:

- If the ticket exists, add its Accura UUID and Tiki ID string to `imported`.
- If it definitely was not created and no request is still running, leave it out
  of `imported`.
- Set `pending` to `null`, then rerun. Do not delete the checkpoint to retry.

The importer does not update or delete existing tickets. A resumed run checks the
source link of each recorded item before skipping it.

Run the integration check against a local binary:

```sh
python3 scripts/test_import_accura.py ./tiki
```

It starts a temporary loopback server with a disposable database and login. It
does not use your server, database, or saved credentials.
