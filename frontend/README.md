# Tiki web

Svelte + TypeScript, built with Vite and embedded in the Go binary for distribution.

## Development

From the repository root:

```sh
make dev
```

Open `http://127.0.0.1:5173`. This installs dependencies when needed and starts Vite plus a Go API on `127.0.0.1:8081`. The first run asks for the password for `dev@tiki.local`; later runs preserve the account and `.dev/tiki.db`. Frontend changes hot-reload, and Go/SQL changes rebuild and restart the API. Failed builds leave the previous API running. Ctrl+C shuts down both processes.

`TIKI_DEV_DB` selects another development database, and `TIKI_DEV_API_PORT` changes the API port. To work against an existing server instead, set `TIKI_API_URL=http://127.0.0.1:8080 make dev`; this skips starting or initializing a local API. Vite proxies `/api` so browser requests stay same-origin. Once dependencies are installed, `npm run dev` in this directory provides the same development workflow.

## Build and checks

```sh
# From the repository root:
make build           # frontend checks + Vite build + bundled CLI downloads + Go binary
make check           # frontend build + Go race tests + vet + bundled binary
npm --prefix frontend test
```

`npm run build` in this directory builds only the frontend. `make build` performs both builds in order. The resulting binary serves the UI at `/` alongside `/api/v1`, with revalidated HTML and immutable fingerprinted assets. It runs without Node or external frontend files. API-only development builds use `go build -tags dev .` and do not require `dist/`.

Browser tests use isolated API fixtures and Vite's `test` mode, which never starts an API or initializes a database. Set `PLAYWRIGHT_PORT` to use an isolated test port alongside an existing development server. Install Chromium with `npx playwright install chromium`, or set `PLAYWRIGHT_CHROMIUM_EXECUTABLE` to an existing executable. `npm run preview` previews a completed frontend build; point `TIKI_API_URL` at an existing API when using it.

## Interaction

The board keeps one ticket in the Tab order; arrows move between tickets without tabbing through every card. Focus follows moved tickets and falls back to a visible card or column when filters hide them. Empty columns never keep a different column's ticket selected.

| Motion | Keys |
| --- | --- |
| Previous / next ticket | ↑ / ↓ or K / J |
| Previous / next column | ← / → or H / L |
| First / last loaded ticket in column | Home / End or gg / G |
| Jump to a visible column | 1–7 |
| Open ticket | Enter |
| Create in current column | C |
| Edit title / description | E or I / D |
| Assign / assign or unassign yourself | A / M |
| Change status / tags / type | S / T / Y |
| Reorder within column | Alt+↑/↓ or Shift+K/J |
| Move to adjacent status | Alt+←/→ or Shift+H/L |
| Search loaded tickets / focus first result | /, then Enter or ↓ |
| Command palette / shortcut guide | Ctrl/Cmd+K / ? |
| Refresh and apply current order | R |
| Previous / next open ticket | [ / ] or K / J outside a field |
| Switch board and details (or search) | F6 |
| Save / save and close | Ctrl/Cmd+Enter / Ctrl/Cmd+Shift+Enter |

Letter shortcuts pause while typing or choosing a dropdown value. Escape closes a popup, leaves an editor field, then closes the panel; unsaved edits block closing or switching tickets. On narrow screens, Tab stays inside details and F6 returns to the board when the draft is clean.

On the board, property shortcuts open searchable action menus and save the chosen change immediately. In details, they focus the corresponding field; M changes the assignment draft. Menus accept arrows, Ctrl+J/K, Ctrl+N/P, or Alt+J/K and Enter. Escape returns focus to the trigger. Quick changes and failed saves retain version checks and never retry writes automatically.

Inline creation uses Enter to add another ticket, Shift+Enter for a line break, Ctrl/Cmd+Enter to add and open details, and Escape to cancel. Current tag/assignee filters become defaults. Tags still being typed are included when saving the editor.

Drag above or below a card to reorder, including across statuses; dropping on a column changes status. Cross-column card drops use a status update followed by a priority move and report a partial failure if only the first succeeds. Ordering uses the API's workspace-wide before/after semantics.

One `/api/v1/board` request loads the initial tickets, users, and tags. Active columns start with up to 100 tickets each; backlog, complete, and void start with 20 each. Selecting a status explicitly raises its preview to 100. Each column retains an explicit Load more control. A normal session restore uses three API requests: `/auth/me`, `/events`, and `/board`; directories larger than 200 entries and a directly opened ticket need additional requests. Text filtering, first/last jumps, and next/previous details cover loaded items only. Tag and assignee filtering use the API. Filters and open items are URL state; conflicts preserve drafts and offer explicit reconciliation.

## Live updates and authentication

The browser keeps one authenticated SSE connection to `/api/v1/events`. Each committed change pushes the current ticket, including its version and description. The UI merges newer versions into existing cards and open details without refetching them, preserves keyboard focus, and keeps unsaved drafts separate. Ordinary saves use the HTTP response the same way; duplicate stream delivery is harmless. Priority changes preserve the visible order until you choose **Apply order**.

There is no periodic list polling. Idle connections receive a small heartbeat every 15 seconds. Notifications are coalesced for 100 ms. A membership or priority change inside a partially loaded column refreshes only that column, preserving its cards while the response is pending. Edits beyond its loaded boundary do not trigger a refetch. Initial connections, reconnects, global priority rebalances, and oversized update bursts request a fresh snapshot. Reconnects back off; hidden tabs disconnect and resync when shown. Expired/revoked sessions return to sign-in while preserving the draft.

Authentication currently uses the existing bearer-token API. The session token is kept in tab-scoped `sessionStorage` (memory only if storage is unavailable). No password is persisted. HttpOnly browser cookies and CSRF protection require backend support; the frontend does not invent a separate authentication contract.


Admins can create single-use invite links through **Manage people** in the toolbar or command menu, then **Invite people**. The link opens a join form where recipients choose their name, email, and password; successful claims sign them in and remove the invite fragment from browser history. An expired, used, or revoked link cannot create another account. Links created in development use Vite's browser address, which proxies the public invite API just like other requests.

The People dialog also supports searching the directory, changing roles, and removing access. Removal requires confirmation in the dialog and retains identity for ticket history. Removed users cannot receive new assignments; use a new invite to restore access. Changes arrive through the existing live directory updates. Members and viewers cannot access management controls, and the server enforces the same permissions.

**Install CLI** is available to every signed-in user in the toolbar and command menu. The dialog loads available platforms only when opened, offers a command scoped to the current browser origin, and provides a separate sign-in command using the user's email. It never copies the browser's session token. `make dev` builds the downloads once; use `make cli` to refresh them after CLI changes without restarting Vite.
