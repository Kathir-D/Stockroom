<h1 align="center">Stockroom</h1>

<p align="center">
  <strong>Barcode-driven equipment checkout for a school media department.</strong><br>
  Scan a student ID to sign in, scan a sticker to check gear in or out, and always know who has what.<br>
  Runs entirely on one machine, with no internet connection needed for daily use.
</p>

<p align="center">
  <a href="https://github.com/Kathir-D/Stockroom/actions/workflows/tests.yml"><img alt="tests" src="https://github.com/Kathir-D/Stockroom/actions/workflows/tests.yml/badge.svg"></a>
  <a href="LICENSE"><img alt="license: AGPL-3.0" src="https://img.shields.io/badge/license-AGPL--3.0-blue.svg"></a>
  <img alt="Go 1.25" src="https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white">
  <img alt="Svelte 5" src="https://img.shields.io/badge/Svelte-5-FF3E00?logo=svelte&logoColor=white">
  <img alt="PostgreSQL" src="https://img.shields.io/badge/PostgreSQL-17-336791?logo=postgresql&logoColor=white">
</p>

---

## Contents

- [What Stockroom is](#what-stockroom-is)
- [Features](#features)
- [How it works](#how-it-works)
- [Requirements](#requirements)
- [Quick start](#quick-start)
- [The `dev` script](#the-dev-script)
- [Configuration](#configuration)
- [Using Stockroom](#using-stockroom)
- [Barcode scanners](#barcode-scanners)
- [Project layout](#project-layout)
- [Development](#development)
- [HTTP API](#http-api)
- [Backup and restore](#backup-and-restore)
- [Troubleshooting](#troubleshooting)
- [Project status](#project-status)
- [Contributing](#contributing)
- [License](#license)

---

## What Stockroom is

A school media department owns cameras, lenses, lights, microphones, tripods and a drawer full of
batteries and SD cards. Students borrow them. Historically that was a clipboard, and the clipboard
does not tell you who still has the 70–200mm.

Stockroom replaces the clipboard. Every physical item wears a barcode sticker encoding its serial
number; every student ID card already carries a 6-digit number. Both go through the same USB
scanner, and the software decides what a scan means from the screen you are on:

- **On the sign-in screen**, a scan is a login. No password, no typing, no queue.
- **Anywhere else**, a scan is an item. If the item is checked out, it is checked back in on the
  spot. If it is available, its detail card opens so it can be added to a cart.

Everything runs on a single dedicated PC in the equipment closet. There is no cloud service, no
account to register, and nothing that breaks when the school's internet does. The only network
traffic Stockroom ever makes is an optional nightly backup push.

### What it deliberately does not do

Reservations and future bookings. Physical locations — "where is it" is answered by "who has it".
Free-form tags, saved filters, per-asset custom fields. LAN or multi-station access: the web app
is bound to localhost on purpose. Native mobile apps. Email or SMS reminders, which would need the
internet the rest of the system is built to live without.

Those are scope decisions, not missing work. See §2 of [`CLAUDE.md`](CLAUDE.md).

---

## Features

**Checkout loop**

- Sign in by scanning a student ID; typing the same number instead requires a password
- Filterable catalogue — Type → Category → Model, plus free-text search
- A cart: gather several items, then check them all out in one transaction
- A due date chosen at checkout, capped at 7 days out
- Check-in by scanning, with an optional damage note. Anyone signed in can return anything
- Full custody history on every asset and every user

**Guard rails**

- Overdue borrowers are blocked from new checkouts — enforced in the API, not just greyed out in
  the UI — with an explicit per-checkout admin override
- The current holder of an item is visible to every signed-in user, by name; their student number
  is not, because that number is a working password-free login
- Sessions expire on idle (10 minutes by default), measured from the last interaction
- A failsafe admin account in `.env` is recreated on every server start, so a broken UI can never
  lock you out of the admin panel

**Administration**

- Asset CRUD, photos, and an `unavailable` status for broken or missing gear
- Category tree editing, with sibling ordering that follows the department's own inventory document
- User management, password resets, and a roster CSV import (optionally with profile photos)
- Live "who has what" and overdue lists
- One-click CSV export of every table

---

## How it works

Three processes, one machine:

```
┌──────────────────────┐   ┌──────────────────────┐
│  Wails desktop app   │   │  Web app (Vite)      │   Both render the same
│  native window       │   │  localhost:5173      │   <StockroomApp> component
└──────────┬───────────┘   └──────────┬───────────┘   from packages/ui
           │                          │
           └───────── fetch ──────────┘
                        │  JSON over HTTP, localhost only
                        ▼
           ┌────────────────────────────┐
           │  Go API server  :8080      │   server/          — HTTP handlers
           │                            │   internal/stockroom — ALL the logic
           └────────────┬───────────────┘
                        │  pgx, direct connection
                        ▼
           ┌────────────────────────────┐
           │  PostgreSQL  :54322        │   hosted by the Supabase CLI stack
           │  (Supabase local stack)    │   (Docker); Studio on :54323
           └────────────────────────────┘
```

Three properties are worth calling out, because they explain most of the layout:

**The Go server is the only database client.** Every query, transaction and permission check lives
in `internal/stockroom`. The frontends hold no SQL, no Supabase client and no credentials — they
call HTTP endpoints and render what comes back. A rule enforced in the UI only is a rule that a
`curl` command ignores, so authorisation is checked inside the package, below the HTTP layer.

**Supabase is used as a Postgres distribution, nothing more.** Migrations, seeding and the Studio
table editor are genuinely convenient; its REST API and Auth service are unused, and row-level
security is off because nothing but the Go server ever connects.

**The frontend exists once.** `packages/ui` (`@stockroom/ui`) holds every component, screen, store
and the API client. `desktop-app/frontend/src/App.svelte` and `web-app/src/App.svelte` each render
`<StockroomApp>` and contain nothing else, so the two UIs cannot drift apart.

---

## Requirements

| Dependency | Why | Install |
|---|---|---|
| **Go** 1.25+ | The API server and the desktop app's Go host | <https://go.dev/dl/> |
| **Node.js** 22+ (with npm) | Frontend tooling for both apps | <https://nodejs.org/en/download> |
| **Docker Desktop** | Runs the local Supabase stack | <https://www.docker.com/products/docker-desktop/> |
| **Supabase CLI** | Starts Postgres, applies migrations, runs pgTAP | <https://supabase.com/docs/guides/cli/getting-started> |
| **Wails CLI** | Builds and runs the desktop app | *installed automatically* |

You do not need to install Wails by hand — `dev.sh` runs `go install` for it if it is missing.

Developed on macOS, deployed on Windows. Both scripts exist; the PowerShell one has not yet been
exercised on real Windows hardware.

---

## Quick start

```bash
git clone https://github.com/Kathir-D/Stockroom.git
cd Stockroom
./scripts/dev.sh
```

That single command checks your tools, starts Docker and the Supabase stack, installs the npm
workspace and Go modules, creates a `.env` from the example if you have none, starts the API
server, **waits for `/health` to actually answer**, then launches both frontends and opens the
browser tabs once their URLs respond.

On Windows, run `scripts\dev.ps1` instead (right-click → *Run with PowerShell*, or
`powershell -ExecutionPolicy Bypass -File scripts\dev.ps1`). On macOS you can also double-click
`scripts/Start Stockroom.command` in Finder; the first time, Gatekeeper may need a right-click →
**Open** → **Open** to approve it.

Press **Ctrl+C** to stop. Your data is preserved between runs.

### Sign in

The seed creates two development accounts. Both can sign in by *scanning* their number, or by
typing it and entering the password `password`:

| Student number | Name | Role |
|---|---|---|
| `123456` | Admin Admin | admin |
| `234567` | Student Student | student |

> [!WARNING]
> These are development credentials for a localhost-only stack. A real deployment gets its accounts
> from the roster import and the `.env` failsafe admin — never from `seed.sql`.

### Where things are

| Service | URL |
|---|---|
| Web app | <http://localhost:5173/> |
| Desktop app (Wails dev server, viewable in a browser) | <http://localhost:34115/> |
| API health check | <http://127.0.0.1:8080/health> |
| Supabase Studio (table editor) | <http://127.0.0.1:54323/project/default> |

The desktop app also opens as its own native window.

> [!IMPORTANT]
> **The web app's port is pinned to 5173** (`vite --strictPort`). The API server's CORS allow-list
> contains 5173 and nothing else, so letting Vite drift to 5174 would fail every preflight — and
> because a blocked preflight and a dead server raise the *same* error in the browser, the UI would
> report "cannot reach the Stockroom server" while the server was perfectly healthy. Vite now
> refuses to start on a busy port instead, which says what is actually wrong.

---

## The `dev` script

There is one script. Everything you do to a working copy is a subcommand of it.

| Command | What it does |
|---|---|
| `./scripts/dev.sh` <br> `./scripts/dev.sh up` | The whole environment, start to finish |
| `./scripts/dev.sh deps` | npm workspace + Go modules + git hooks, nothing else |
| `./scripts/dev.sh test` | Every suite — the same thing the pre-commit hook and CI run |
| `./scripts/dev.sh stop` | Stop Supabase and anything still holding a Stockroom port |
| `./scripts/dev.sh status` | What is running right now, and what is not |
| `./scripts/dev.sh --help` | The above, from the script itself |

`up` accepts `--no-desktop`, `--no-web`, `--no-open` (skip the browser tabs) and `--ci` (install
from the lockfile exactly). In PowerShell they are `-NoDesktop`, `-NoWeb`, `-NoOpen`, `-Ci`.

**`up` is safe to re-run after a crash.** If the API server is already up and healthy it reuses it
rather than starting a second copy that cannot bind the port; if something holds the port but fails
the health check, it says so and stops instead of launching a UI at a broken backend. It tears down
only what it started — a Supabase stack that was already running when you invoked it is still
running after you press Ctrl+C. Each service tees its output to `.run-logs/` (gitignored), so a
crash three minutes in leaves something to read.

### Running the pieces by hand

<details>
<summary>Manual setup, if you would rather not use the script</summary>

```bash
cp .env.example .env        # first time only; see Configuration below
./scripts/dev.sh deps       # npm workspace + Go modules + git hooks
supabase start              # Postgres :54322 and Studio :54323, migrations and seed applied
go run ./server             # the API; check: curl http://127.0.0.1:8080/health
npm run dev:web             # web app on :5173
cd desktop-app && wails dev # desktop app, native window
supabase stop               # when you are done; data is preserved
```

The Go module is at the repository root, so `go build ./...` from there builds the server, the
desktop app's Go side and `internal/stockroom` together.

</details>

> [!CAUTION]
> **Run every npm command from the repository root.** This is an npm workspace: `packages/ui`,
> `web-app` and `desktop-app/frontend` share one `package-lock.json` and one installed tree. Running
> `npm install` *inside* one of the apps gives that app its own copy of Svelte and Vite, and the
> failure is silent and baffling — components render, but their state stops updating.
>
> `dev.sh deps` detects and repairs that, and both `up` and `test` call it, so you rarely have to
> think about it. (A `node_modules/.vite` directory under an app is *not* the problem; that is just
> Vite's dependency cache.)

---

## Configuration

Copy `.env.example` to `.env` at the repository root.

| Variable | Purpose | Default |
|---|---|---|
| `DATABASE_URL` | Direct Postgres connection | `postgresql://postgres:postgres@127.0.0.1:54322/postgres` |
| `SERVER_ADDR` | API listen address | `127.0.0.1:8080` |
| `ADMIN_STUDENT_NUMBER` | Failsafe admin account; digits only | *(none)* |
| `ADMIN_PASSWORD` | Failsafe admin password; 8–72 characters | *(none)* |
| `UPLOADS_DIR` | Where profile and asset photos are copied | `./uploads` |
| `BACKUP_DIR` | Local CSV export target | *(none)* |
| `SESSION_IDLE_MINUTES` | Idle timeout, measured from the last interaction | `10` |

**The failsafe admin is best-effort by design.** If those two values are blank, malformed or
rejected, the server logs a warning and starts anyway. A typo in `.env` must never take the whole
API down — but it does mean the account only exists when you have filled them in, which matters for
disaster recovery.

**`SERVER_ADDR` is load-bearing.** The frontends hardcode `http://127.0.0.1:8080`, so pointing the
server anywhere else silently breaks them. `dev.sh up` warns when it notices.

---

## Using Stockroom

### The student loop

1. **Scan your ID card.** You are signed in. (Type the number by hand instead and you will be asked
   for a password; scanning is the fast path on purpose.)
2. **Find what you need.** Filter down the category tree on the left, or search.
3. **Add to cart.** Click an item, read the detail card, add it. Nothing is checked out yet.
4. **Check out.** Pick a due date — at most 7 days out — and the whole cart moves to `checked_out`
   in one transaction. You are then offered a sign-out prompt, because the closet PC is shared.
5. **Bring it back.** Scan the item's sticker from any screen. It is checked in immediately, and you
   can add a damage note afterwards.

A borrower with anything overdue sees a warning at sign-in, and checkout is disabled until the
overdue item comes back. An admin can override that for a single checkout.

### First sign-in for a new account

Accounts created by the roster import, or by an admin in the panel, start with **no password**. The
first time such a person *scans* their card, they are asked to set one before they can do anything
else — until then, typed login is impossible. Admins can also set a password directly from the panel.

### The admin panel

Admins get everything above, plus assets and photos, the category tree, users and the roster CSV
import, password resets, live active-custody and overdue lists, checkout on behalf of another
person, the overdue override, and a "Backup Now" CSV export.

For bulk edits or anything the panel does not cover, **Supabase Studio**
(<http://127.0.0.1:54323/project/default> → Table Editor) is a full Postgres table editor over the
same database.

---

## Barcode scanners

Stockroom expects a plain **USB HID keyboard-wedge** scanner: it types the code into whatever has
focus and presses Enter. No SDK, no driver, no vendor software. If a scanner needs its own
application to work, it is the wrong scanner.

Two kinds of barcode flow through the same keyboard buffer:

| Barcode | Contents | Meaning |
|---|---|---|
| Student ID card | 6-digit student number | Sign in |
| Item sticker | `assets.serial_number` | Check in, or open the detail card |

Which one a scan means is decided by the active screen, never guessed from the payload. The backend
is told which it is.

**Telling a scan from typing** is a matter of speed: a scanner emits keystrokes a few milliseconds
apart, a person does not. The threshold lives in one place —
`SCAN_KEY_THRESHOLD_MS` in `packages/ui/src/lib/scanner.ts`, currently 50 ms — so tuning it against
real hardware is a one-line change. Press **Ctrl+Shift+D** anywhere in the app to print the last
burst's inter-key timings to the browser console.

The keystroke buffer also watches for editing: Backspace moves it, and any caret key or editing
chord (`Alt/Cmd+Backspace`, `Cmd+A` then overtyping, paste) discards the pending burst entirely.
The sign-in screen goes further and only accepts a burst as a *scan* when the field on screen holds
exactly what the buffer saw. This is not fussiness — a scan signs somebody in with no password, so
a buffer that has drifted from the visible field is the difference between "nothing happened" and
"the wrong student was signed in".

Linear stock (batteries, bags, SD cards) uses model-prefixed serials — `T7IBAT-001`, `SD-014` — so
that every physical unit has its own scannable identity.

---

## Project layout

```
stockroom/
├── internal/stockroom/     ALL business logic; the only code that touches Postgres
│   ├── db.go               the pool, the session store, the configured directories
│   ├── auth.go             Actor, RequireAdmin, RequireFullSession, login/logout
│   ├── users.go roster.go  user CRUD, password resets, roster CSV import
│   ├── categories*.go      the category tree, and its admin CRUD
│   ├── assets*.go          browse and admin asset operations
│   ├── custody.go          ScanItem, CheckOutAssets, CheckInAsset, histories
│   └── backup.go           CSV export
├── server/                 net/http JSON API; handlers decode, call the package, encode
├── packages/ui/            @stockroom/ui — every component, screen, store and the API client
│   └── src/lib/
│       ├── app.svelte      the whole application: routing, the scan listener, the 401 hook
│       ├── api/            one function per endpoint, over one fetch client
│       ├── scanner.ts      keystroke buffer, scan-vs-typed, the Ctrl+Shift+D diagnostic
│       ├── components/     shadcn-svelte primitives + Stockroom components
│       └── screens/        sign-in, browse, cart, history, admin/*
├── desktop-app/            Wails host (Go side is a window, nothing more)
├── web-app/                Vite + Svelte 5 host
├── supabase/               config.toml, migrations/, seed.sql, tests/ (pgTAP)
├── scripts/                dev.sh, dev.ps1, Start Stockroom.command
├── docs/                   adr/ · design/ · agents/
└── uploads/                photos, served at /files/ (gitignored)
```

Further reading, in rough order of usefulness:

| Document | What it covers |
|---|---|
| [`CLAUDE.md`](CLAUDE.md) | The master reference: architecture, schema, permissions, endpoint table, and a decision log with the reasoning behind each choice |
| [`CONTEXT.md`](CONTEXT.md) | Domain glossary — the vocabulary the code and the docs both use |
| [`TODO.md`](TODO.md) | Phase-by-phase build plan and current progress |
| [`TESTING.md`](TESTING.md) | What is tested, what was deliberately cut, and why |
| [`CI.md`](CI.md) | The GitHub Actions job, branch protection, the pre-commit hook |
| [`docs/design/design-system.md`](docs/design/design-system.md) | The UI: tokens, components, screen specs |
| [`docs/design/backup.md`](docs/design/backup.md) | The full backup and restore design |
| [`docs/adr/`](docs/adr/) | Architecture decision records |

---

## Development

### Tests

```bash
./scripts/dev.sh test
```

Runs `go vet`, the Go tests, pgTAP against the local Postgres, `svelte-check`, Vitest and both
frontend builds — the same steps CI runs, in the same order.

The database suites need the Supabase stack running. If Postgres is down, the Go tests that need it
and the pgTAP run are reported as **FAILED**, not skipped, on purpose: a silent skip that looks like
a pass is worse than a red run.

The suite is deliberately small — one happy path and one permission gate per module.
[`TESTING.md`](TESTING.md) lists what runs, what was cut and what is worth adding next.

### Pre-commit hook

`.githooks/pre-commit` runs the full suite before a commit is created, so a red build is caught on
the machine that broke it. `dev.sh deps` installs it by pointing `core.hooksPath` at the tracked
`.githooks/` directory, which is why the hook lives in the repository rather than in `.git/hooks`: a
hook nobody can see is a hook nobody maintains.

Docs-only commits skip it automatically, using the same path list as the CI workflow. `git commit
--no-verify` and `STOCKROOM_SKIP_TESTS=1` are the deliberate escapes; CI runs the same suite on the
pull request, so skipping the hook delays a failure rather than hiding it.

### Database changes

Migrations live in `supabase/migrations/` and are applied automatically by `supabase start` and
`supabase db reset`. `supabase/seed.sql` loads on reset and builds the 63-node category tree from
[`Catagories.md`](Catagories.md), which is the source of truth for the department's inventory
structure — including the order categories appear in, which the browse screen follows.

### House rules

A few conventions that the code assumes and reviews enforce:

- **Permission checks belong in `internal/stockroom`**, not in `server/` and certainly not in the
  UI. The router is a transport; it is not the security boundary.
- **One writer per `photo_path` column.** `SetAssetPhoto` for assets, the roster import for
  profiles. A form field that can write the column can also point it at a file nobody uploaded.
- **Design tokens live in `packages/ui/src/lib/styles/tokens.css`.** It is the only file allowed to
  define a colour, radius, shadow or duration. No component CSS files, no `tailwind.config.js`.
- **Every decision that cost an argument gets a line in `CLAUDE.md` §13**, with the reasoning and
  the alternative that lost. The log is the point of the file.

---

## HTTP API

All routes are JSON over `http://127.0.0.1:8080`, localhost only. Authentication is a session token
returned by either login route and sent as `Authorization: Bearer <token>` or as the HttpOnly
`stockroom_session` cookie. "Admin" below is enforced inside `internal/stockroom`, not by the
router.

In the **Who** column: *anyone* needs no session · *any session* includes a password-less "limited"
one (§7) · **any full** is every signed-in user whose password is set — a limited session gets `403
{"error":"password not set","needs_password":true}` from these.

| Route | Who | Notes |
|---|---|---|
| `GET /health` | anyone | Pings Postgres |
| `POST /auth/scan` · `POST /auth/password` | anyone | `{student_number}` / `{student_number, password}` |
| `POST /auth/set-password` · `POST /auth/logout` · `GET /me` | any session | Reachable from a password-less "limited" session |
| `GET /categories/tree` | any full | Nested Type → Category → Model, in `sort_order` |
| `GET /assets?category=&status=&q=` · `GET /assets/{id}` | any full | Rows carry `category_path`, `photo_url` and the current holder |
| `POST /scan` | any full | `{serial}` — checked out ⇒ checked in; available ⇒ detail |
| `POST /checkout` | any full | `{asset_ids, due_at, custodian_id?, override_overdue?}`; the last two are admin-only |
| `POST /assets/{id}/checkin` | any full | Optional `{note}` |
| `POST /custody/{id}/note` | any full | Adds a damage note to a *closed* custody event |
| `GET /users/{id}/history` | self or admin | |
| `GET /custody/active` · `GET /custody/overdue` · `GET /assets/{id}/history` | admin | |
| `GET/POST /users` · `GET/PUT/DELETE /users/{id}` · `POST /users/{id}/password` | admin | |
| `POST /users/import` | admin | Roster CSV, multipart or `text/csv` |
| `POST /assets` · `PUT/DELETE /assets/{id}` · `POST /assets/{id}/status` | admin | `serial_number` required; `asset_tag` is generated |
| `POST /assets/{id}/photo` | admin | Multipart, 10 MB, `.jpg .jpeg .png .gif .webp` |
| `POST /categories` · `PUT/DELETE /categories/{id}` | admin | Depth capped at 3 |
| `POST /admin/backup` | admin | CSV export into `BACKUP_DIR` |
| `GET /files/...` | anyone | Photos; `<img>` tags cannot send a bearer token |

Error mapping: `404` not found · `400` invalid · `401` unauthorised or bad credentials · `403`
forbidden · `409` conflict or overdue-blocked · **`503` not configured** (an unset `UPLOADS_DIR` or
`BACKUP_DIR`, with the message passed through — that is neither the client's fault nor a bug, and
the admin reading it is the person who edits `.env`) · `500` for everything else, with the detail
logged rather than sent.

Full table with request and response shapes: [`CLAUDE.md`](CLAUDE.md) §8.1.

---

## Backup and restore

**What works today.** "Backup Now" in the admin panel exports every table to
`BACKUP_DIR/<yyyy-mm-dd>/<table>.csv` through `COPY … TO STDOUT`, in a single repeatable-read
snapshot, staged and renamed into place. It is local-only and manual.

> [!WARNING]
> Those CSVs **cannot be restored yet.** Reloading them as they stand would replay in foreign-key
> violating alphabetical order, fire the asset status trigger on every row, and lose the asset-tag
> sequence position. Treat the current export as a data escape hatch, not as disaster recovery.

**What is designed and not yet built** ([`docs/design/backup.md`](docs/design/backup.md)): a
scheduler goroutine inside the API server, nightly CSV plus a restorable zip, two selectable
off-site targets — Google Drive via `rclone` and GitHub via its REST API, both at once when both
are configured — a local generational photo mirror, staleness warnings surfaced at sign-in, and a
restore that is a feature of the admin panel rather than a shell script. A `cmd/restore` CLI is
specified as the guaranteed floor beneath it, because the panel route depends on an admin account
existing and the failsafe admin is deliberately optional.

The restore validates before it commits: foreign keys re-checked by anti-join, row counts against
the manifest, per-file checksums, and every sequence proven to resume above its column's maximum.
`setval` is not transactional, so sequences are written only after every other check has passed.

---

## Troubleshooting

<details>
<summary><strong>The UI says it cannot reach the Stockroom server</strong></summary>

Check `./scripts/dev.sh status` first. A blocked CORS preflight and a dead server raise the same
error in the browser, so the message cannot tell them apart. The usual causes, in order: the API
server is not running; Docker or Supabase is down so `/health` fails; or the web app is on a port
other than 5173 and is failing the CORS allow-list.

</details>

<details>
<summary><strong>Components render but nothing updates when I interact</strong></summary>

That is the two-copies-of-Svelte failure, and it means a nested `node_modules` is shadowing the
workspace. Run `./scripts/dev.sh deps`, which detects and repairs it. Always install from the
repository root.

</details>

<details>
<summary><strong>Port 5173 is in use and Vite refuses to start</strong></summary>

Deliberate — see the note in [Quick start](#where-things-are). Free the port
(`./scripts/dev.sh stop`, or `lsof -ti:5173 | xargs kill`) rather than letting Vite pick another
one, or every request will fail the CORS preflight.

</details>

<details>
<summary><strong>"Something holds :8080 but does not answer /health"</strong></summary>

An API server is running without a working database — usually Docker or Supabase stopped underneath
it. `./scripts/dev.sh stop`, then `./scripts/dev.sh up`.

</details>

<details>
<summary><strong>A scan asks for a password instead of signing me in</strong></summary>

The burst was read as typed. Either the keystroke gaps exceeded `SCAN_KEY_THRESHOLD_MS` — press
Ctrl+Shift+D to see the actual timings and tune the constant — or the field on screen no longer
matched what the scanner sent, which happens if the field was edited mid-scan. Clear the field and
scan again.

</details>

<details>
<summary><strong>I am locked out of the admin panel</strong></summary>

Set `ADMIN_STUDENT_NUMBER` and `ADMIN_PASSWORD` in `.env` and restart the server. That account is
recreated with that password on every start, by design, precisely for this.

</details>

<details>
<summary><strong>I want to start over with a clean database</strong></summary>

`supabase db reset` reapplies every migration and reloads `seed.sql`. It destroys all local data.

</details>

---

## Project status

Stockroom is coursework built for a real department, on a nine-week schedule, and it is not finished.

**Done.** The schema and seed. The Go API: authentication, sessions, permissions, the roster
import, browse and filtering, the full scan/cart/checkout/check-in loop, custody history, overdue
handling, and the admin endpoints. The entire frontend, as one shared package rendered by both
hosts. CSV export.

**In progress or specified.** The nightly backup and the restore path are designed in detail and
not yet implemented. Real inventory entry is under way. The barcode scanner has not been purchased,
so the scan-vs-typed threshold is an untuned default. Kits — a named bundle checked out as one unit
— are the lowest priority and may not ship.

[`TODO.md`](TODO.md) tracks this phase by phase. [`CLAUDE.md`](CLAUDE.md) §13 records every decision
that shaped it, including the ones that were reversed.

---

## Contributing

Issues and pull requests go to <https://github.com/Kathir-D/Stockroom>.

Before opening a PR:

1. `./scripts/dev.sh deps` — this also installs the pre-commit hook
2. Make your change, with tests where behaviour changed
3. `./scripts/dev.sh test` — the hook runs this anyway, but running it first is faster than
   discovering it at commit time
4. If the change involved a real trade-off, add a line to `CLAUDE.md` §13 explaining the choice and
   the alternative you rejected. That log is how the next person avoids relitigating it.

---

## License

[GNU Affero General Public License v3.0](LICENSE).
