# Stockroom

Stockroom is an equipment checkout system for a school media department. Students scan an ID card to sign in, scan item barcodes to borrow and return, and the system records who has what. It runs on one always-on PC in the camera closet and needs no internet for daily use.

This file describes the system as it stands today. Three other files carry the rest:

- `ROADMAP.md` is the open work. Read it before starting anything new.
- `docs/decisions.md` is why things are the way they are. Read the entry for an area before changing how it behaves. When you settle a question, add a dated entry there and update this file if the system changed.
- `CONTEXT.md` fixes the words. When two words could name one thing, use the one it gives.

`docs/api.md` lists every HTTP route, and `docs/design/` holds the long-form designs (UI, backup, photo wall, closet camera).

## 1. The user flow

1. **Sign in.** Scanning a student ID card signs a student in with no password. Typing the same number asks for a password. An admin always finishes with a password, even after a scan.
2. **Browse.** Filters on the left (Type → Category → Model, plus search), matching items on the right.
3. **Add to cart.** Clicking an item opens its detail dialog, which has Add to cart. The cart lives in the frontend until checkout. Kits add all their units at once.
4. **Check out.** The borrower picks the last day they need the items, at most seven days out unless an admin changed that. Every item in the cart checks out in one transaction. The UI then signs the person out after a ten-second countdown unless they press Keep going.
5. **Scan an item.** Every item carries a barcode sticker holding its serial. Scanning a checked-out item returns it at once, whoever borrowed it. Scanning an available item opens its detail dialog. Scanning a student card on any screen switches to that account.
6. **Admin panel.** Assets, categories, users and roster import, kits, overdue and everything out, Needs attention, backup and restore, settings, the photo wall, and the activity log.

## 2. Scope

The system must know what equipment exists and who has it, check items in and out in seconds, keep the full custody history of every item and every person, and surface overdue loans in the app. Daily use needs no internet. A nightly backup goes off-site to Google Drive, GitHub, or both (§11).

Platforms, in order: a Linux closet PC first, then macOS, then Windows through WSL 2 only. Development happens on macOS.

Out of scope, though some have tables in the schema: reservations, physical locations (an item's location is who has it), tags, saved filters, per-asset custom fields, LAN or multi-station access, mobile apps, email or SMS reminders, GPS.

A kit is a named bundle of units, for example a camera, a lens and a bag. It is a label over assets and holds no custody of its own. Adding one to the cart expands it into its asset ids, so every rule about checkout applies to its units unchanged. An asset belongs to at most one kit (`docs/adr/0002`).

The closet camera is optional and ships off. Frigate watches a webcam over the door, and every visit becomes a walked-in and walked-out row with a local recording (`docs/design/closet-camera.md`). It runs on macOS and Linux only.

## 3. Deployment

Development and production run different stacks on purpose.

**Development** runs the Supabase CLI's Docker stack, but only for Postgres, migrations, `seed.sql`, pgTAP and Studio. Its REST, Auth, Realtime and Storage services are unused. The Go server runs from the repo, and the two frontends (the Wails window and the web app on `localhost:5173`) each render `<StockroomApp>` from `packages/ui` and call the server over HTTP. `scripts/dev.sh` runs all of it.

**An install** is the operating system's PostgreSQL, one binary and one OS service. The binary holds the API, the web UI and every migration. It dumps the database and applies pending migrations at boot, and serves the UI at `/` on the same origin as the API. The service restarts it if it dies, and it is required, because the nightly backup is a goroutine inside the server. Supabase never ships to a school, because it would put Studio on the closet PC with no authentication and full access to the roster.

The binary's subcommands are `serve` (the default), `setup`, `service`, `doctor`, `restore`, `version` and `open`. Each reads the config from `--config`, `STOCKROOM_CONFIG`, a `.env` above the working directory, then the system path, in that order (`LoadConfigFrom`).

- Linux (Debian 12, Ubuntu 24.04 and newer) runs `curl … scripts/get.sh | sudo bash`, which installs the `.deb` from GitHub releases and runs `stockroom setup`. The package depends on the distribution's `postgresql (>= 14)` and `rclone (>= 1.60)` and ships a systemd unit. Setup runs the service as the account that ran `sudo`, creates the `stockroom` role and database, and writes `/etc/stockroom/stockroom.env`. Data lives in `/var/lib/stockroom`.
- macOS runs `brew install kathir-d/stockroom/stockroom && stockroom setup`. The cask depends on Homebrew's `postgresql@17` and `rclone`, and setup installs two LaunchDaemons so both start at boot with nobody logged in. Config and data live in `$(brew --prefix)/var/stockroom`.
- Windows runs `scripts/get.ps1`, which installs Ubuntu under WSL 2, turns systemd on, runs `get.sh` inside it, and registers a `Stockroom WSL` boot task. There is no native Windows installer or service.

`docs/INSTALL.md` is the guide. `.goreleaser.yaml` builds every release artefact and `packaging/linux/` holds the unit and maintainer scripts. The older installer, `scripts/install.sh` with a `postgres:17` container (`deploy/`), still works from a checkout. It gets deleted, with `deploy/docker-compose.yml`, `deploy/stockroom-run.sh` and the two service templates, once the packages pass on a real Linux machine and a real Mac. `deploy/camera/` stays.

## 4. Backend architecture

The Go server is the only database client. `internal/stockroom` holds every query, transaction, permission check and account rule. `server/` decodes a request, calls the package and encodes the answer. The frontends call it with `fetch` through `packages/ui/src/lib/api/`. No TypeScript holds database credentials, and nothing uses PostgREST or supabase-js.

Rules that follow from this:

- Enforce permissions inside `internal/stockroom`, never only in the router or the UI. `RequireAdmin` and `RequireFullSession` guard every call that needs them, and a hidden field in the UI is still a `fetch` away.
- `DB` (`db.go`) is the one handle. It owns the pool, the session store, `UploadsDir` and `BackupDir`, built by `Open(ctx, url, Options)`.
- Package code returns the sentinels in `errors.go`, and `server/` maps them to statuses (the list is at the end of `docs/api.md`). Anything unmapped is a 500 whose detail is logged and not sent.
- Every action writes its `activity_log` row inside its own transaction, so a refused action leaves no row (`docs/adr/0003`).
- RLS is off. The server connects as `postgres` on port 54322 through `DATABASE_URL`.

## 5. Tech stack

| Layer | Technology |
|---|---|
| Database | PostgreSQL 14 or newer (the CI matrix runs 14 to 17). Supabase CLI in development, the OS package in an install |
| Schema | `supabase/migrations/*.sql`, read by the CLI in development and by `stockroom.Migrate` at boot (embedded through `supabase/embed.go`). Both record into `supabase_migrations.schema_migrations` |
| DB access | Go, `pgx/v5` and `pgxpool` |
| API | Go `net/http` on localhost, JSON. Logic in `internal/stockroom` |
| UI | `packages/ui` (`@stockroom/ui`): Svelte 5, TypeScript, Tailwind CSS v4, shadcn-svelte v1 over Bits UI. Ships raw `.svelte` that each host's Vite compiles |
| Hosts | Wails desktop app (`desktop-app/`) and the Vite web app (`web-app/`), both a few lines around `<StockroomApp>`. The web app's build is embedded in the binary through `web-app/embed.go` |
| Files | Photos in `uploads/`, served at `/files/` |
| Scanner | A USB HID keyboard-wedge scanner (§10) |
| Backup | A goroutine in the server, CSV per table in a zip, pushed with `rclone` to Drive and/or the GitHub REST API (§11) |
| Config | `.env` in development, `stockroom.env` at the system path in an install (§9). Everything an admin can change lives in `app_settings` |
| Packaging | GoReleaser: `.deb` through nfpm, a Homebrew cask, release assets without a version in the name |

## 6. Database schema

The migrations in `supabase/migrations/` are the source. Read them rather than a summary. What they don't say:

- `assets.serial_number` is the scan key, unique and not null. `asset_tag` is generated by a column default and nobody types or reads it. Keep serials short, because the small label fits about 9 characters on Letter and 7 on A4. `GET /labels/layouts` reports each layout's limit.
- `categories` is a three-level tree through `parent_id`: Type → Category → Model. `sort_order` is a row's position among its siblings, and browse follows it rather than the alphabet. A branch may stop short of depth 3 (`Primes` is seeded empty), and an asset may file under any node (`docs/adr/0001`). `categories.name` is unique across the whole table.
- A trigger refuses a serial that equals a student number, and the reverse, ignoring case. A scanned code is therefore an item or a card, never both.
- `profiles.is_admin` is the only permission flag. The `role` column and the `user_role` enum are unused. `password_hash` is null until the owner or an admin sets one.
- `assets.status` uses `available`, `checked_out` and `unavailable`. The open custody row decides whether an item is out, not the status column.
- `activity_log` is append-only. Triggers refuse update, delete and truncate, and it has no foreign keys, so rows outlive the asset or account they name (`docs/adr/0003`).
- `app_settings` is one row holding everything an admin configures: backup targets, the student-number format, the loan rules (due time, longest loan, the overdue block, closed dates), the idle timeout and scanner speed, setup state, the Google client, the photo wall's folder and size. Secret columns are redacted from exports.
- `kits` names are unique ignoring case, and `kit_items.asset_id` is unique.
- Unused tables: `locations`, `tags`, `asset_tags`, `bookings`, `saved_filters`, and `assets.custom_fields` and `assets.location_id`.

## 7. Accounts and permissions

| Capability | Student | Admin |
|---|---|---|
| Sign in, browse, see item details and who holds an item | ✓ | ✓ |
| Check out to self, return any item, add a damage note | ✓ | ✓ |
| See own custody history | ✓ | ✓ |
| Add a kit to the cart, return a whole kit | ✓ | ✓ |
| Check out on behalf of someone, override the overdue block | | ✓ |
| Any item's or person's full history, the active and overdue lists | | ✓ |
| Manage assets, categories, users, kits, passwords, the roster | | ✓ |
| Backup, restore, settings, photo wall, camera, the activity log | | ✓ |

**Who holds an item** is visible to every signed-in user, as a name, a due date and whether it is overdue. The holder's student number is admin-only, because it is a working scan login. Past holders are admin-only.

**Sign-in.**
- A scan (a burst of keystrokes at scanner speed, then Enter) signs a student in with no password. A typed number needs the password.
- An admin's scan asks for the password. An admin with no password yet can't sign in by scan at all, or whoever scanned first would choose it.
- An account with no password gets a limited session on its first scan. That session can only reach `POST /auth/set-password`, `GET /me` and `POST /auth/logout`, and setting the password upgrades it.
- Passwords are 8 to 72 characters. `password_set_by` records who chose the current one.
- Both logins are JSON only and rate limited. Five wrong passwords lock a number for five minutes. The server refuses a request whose `Host` is not a loopback name.
- Archived accounts can't sign in and drop out of pickers, but keep their history. Archiving is how a graduate leaves, because an account that ever borrowed anything can't be deleted.
- The failsafe admin comes from `ADMIN_STUDENT_NUMBER` and `ADMIN_PASSWORD` in `.env`. Every start makes sure that account exists, is an admin, is not archived and has that password. It is best-effort, so a bad value logs a warning and the server still starts.

**Sessions** live in memory, so a restart signs everyone out. A session sends its token as a bearer header or the `stockroom_session` HttpOnly cookie. It expires after `app_settings.session_idle_minutes`, or `SESSION_IDLE_MINUTES` (10) when that is null, counted from the last request. A change applies to live sessions at once. The UI pings the server on real interaction and returns to sign-in when the server drops the session. The actor's profile reloads on every request, so a changed admin flag or a deleted account takes effect at once. The cart survives a page reload and clears on sign-out or timeout.

**Checkout and returns.**
- A loan is due at the closing time (`app_settings.due_time`, 15:30) on the first school day after the last day of use. A school day is a weekday that isn't in `app_settings.closed_dates`, which an admin keeps in Settings. The server moves any `due_at` a client sends to the next closing time on a school day. `due.go` and `due.ts` hold the same rule.
- The last day of use is at most `max_checkout_days` (7) after today.
- A person with anything overdue can't check out. The UI and `CheckOutAssets` both enforce it, and an admin can override it per checkout. `overdue_blocks_checkout` turns the block off for everyone; the overdue notice still shows.
- A damage note, or a student's return with no scan behind it (a typed serial, a Check in button, a whole-kit return), goes to Admin → Needs attention. The item stays available and shows the report until an admin clears it. A student may add a note only within 30 minutes of the return.
- Scanning an item you checked out in the last ten minutes asks "Return it?" instead of returning it.
- Mark lost closes the loan as lost, makes the item unavailable, and stops the borrower being overdue on it.

## 8. Repository layout

```
internal/stockroom/   all business logic, the only code that touches Postgres
server/               the stockroom binary: net/http handlers, the router, the embedded UI at /, subcommand dispatch
internal/setup/       stockroom setup, service and doctor, behind a command runner so tests need no root
internal/cli/         stockroom restore; cmd/restore/ wraps it for go run
internal/platform/    WSL and systemd detection, opening a browser
packages/ui/          every screen, component, store, the API client and the scanner
desktop-app/          Wails host, a window around <StockroomApp>
web-app/              Vite host; its build is embedded in the binary
supabase/             migrations/, seed.sql, tests/ (pgTAP), embed.go
packaging/linux/      the .deb's systemd unit, maintainer scripts and smoke.sh
deploy/               the older checkout install (compose file, service templates) and camera/
scripts/              get.sh and get.ps1 (install), dev.sh (development), install.sh (older install)
examples/             fake example data the setup wizard can load (embedded)
docs/                 api.md, decisions.md, adr/, design/, agents/, and the guides
```

Where things live in `internal/stockroom`:

| Area | Files |
|---|---|
| Handle, config, errors | `db.go`, `config.go`, `errors.go`, `pgerr.go`, `types.go`, `migrate.go`, `premigrate.go`, `rclone.go`, `version.go`, `doctor.go` |
| Accounts | `auth.go`, `sessions.go`, `login_guard.go`, `password.go`, `student_number.go`, `users.go`, `roster.go`, `failsafe.go`, `envfile.go` (the only writer of `.env`), `setup.go` |
| Catalogue | `categories*.go` (`categoryTree` is the only in-memory shape of the table), `assets*.go`, `photos.go`, `barcode.go`, `labels.go` |
| Custody | `custody.go` (`openCustodySQL` is the one definition of "out"), `due.go`, `review.go`, `kits.go` |
| Backup | `backup.go`, `export.go` (`takeSnapshot` is the one archive builder), `archive.go`, `restore.go` (the one `RestoreFromZip`), `backup_status.go`, `scheduler.go`, `photos_backup.go`, `target*.go`, `settings.go` |
| Google | `google.go`, `google_admin.go` (one Drive connection shared by backup and photo wall), `drive_authorize.go`, `local_folders.go` |
| Photo wall | `photowall*.go` (the set in memory, the Drive source, the normalizer, the admin reads) |
| Activity and camera | `activity.go`, `camera*.go` |

In `packages/ui/src/lib`, `app.svelte` is the whole app (routing, the one scan listener, the 401 hook), `scanner.ts` tells a scan from typing, and `styles/tokens.css` is the only file allowed to define a colour, radius, shadow or duration. `docs/design/design-system.md` is the UI reference, and where it disagrees with this file about behaviour, this file wins.

Install npm packages at the repo root only (`./scripts/dev.sh deps`). A `node_modules` inside `web-app` or `desktop-app/frontend` makes a second copy of Svelte, and components then render but never update.

Adding a route means a handler in `server/`, a line in `server/router.go`, a function in `packages/ui/src/lib/api/index.ts`, and a row in `docs/api.md`.

## 9. Development setup

`./scripts/dev.sh` (macOS or Linux) starts the database, the server and both frontends, and Ctrl+C stops them and keeps the data. Its other subcommands are `deps`, `test`, `stop` and `status`. `dev.sh deps` also points `core.hooksPath` at `.githooks/`, whose pre-commit hook runs the suite. Go tests run with `-p 1`, because the restore tests truncate the shared database. On Windows, develop inside Ubuntu under WSL 2. `TESTING.md` and `CI.md` cover the suite and CI.

By hand: `supabase start`, then `go run ./server`, then `npm run dev:web` or `cd desktop-app && wails dev`. `supabase start` applies migrations and `seed.sql`, which creates two development accounts, both with the password `password`:

| Student number | Name | Role |
|---|---|---|
| `123456` | Admin Admin | admin |
| `234567` | Student Student | student |

Neither account starts without a password, so to exercise the first-scan password flow, create a user in the admin panel and scan their number.

`.env` (copy `.env.example`). An install reads the same keys from `/etc/stockroom/stockroom.env` or `$(brew --prefix)/var/stockroom/stockroom.env`, which setup writes, and refuses relative data paths there:

| Var | Purpose | Local default |
|---|---|---|
| `DATABASE_URL` | direct Postgres connection | `postgresql://postgres:postgres@127.0.0.1:54322/postgres` |
| `SERVER_ADDR` | listen address | `127.0.0.1:8080` |
| `ADMIN_STUDENT_NUMBER`, `ADMIN_PASSWORD` | the failsafe admin (§7) | none |
| `UPLOADS_DIR` | photos | `./uploads` |
| `SESSION_IDLE_MINUTES` | idle timeout from the last request, unless Admin → Settings sets one | `10` |
| `PRE_MIGRATE_DUMP`, `PRE_MIGRATE_DIR`, `PG_DUMP` | the dump before a migration: `required` or `off`, where, and which `pg_dump` | `off` |
| `RCLONE_BINARY` | rclone's path, when `PATH` doesn't have it | found at start |
| `BACKUP_DIR`, `PHOTO_BACKUP_DIR`, `RCLONE_REMOTE`, `SIGNIN_PHOTOS_FOLDER_ID` | first-boot seeds only | none |

The last row seeds empty `app_settings` columns on the first start against a fresh database, and is ignored after that. The admin panel owns those values, so editing `.env` later changes nothing. That is deliberate: a value an admin typed must never revert on a restart.

## 10. Barcode scanner

A USB HID keyboard-wedge scanner types the code and then Enter into whatever has focus. It needs no driver. Two kinds of code arrive through the same keyboard buffer: student cards and item serials.

`packages/ui/src/lib/scanner.ts` decides whether a burst was a scan or typing. A scan's keys arrive within `app_settings.scan_threshold_ms` (50 ms, untuned, set in Admin → Settings and sent in `/signin/config`) of each other. Any edit (Backspace, a chord, a caret key) makes the burst typed, and the sign-in screen treats a burst as a scan only when the field's value matches it. Ctrl+Shift+D shows a diagnostic for tuning against real hardware.

Which endpoint a code goes to depends on the screen and a lookup, never a guess. On the sign-in screen a code is a login. On other screens it is an item scan, unless no item has that serial and it has the shape of a student number, in which case it switches accounts. Keep a visible input focused, so typing always works as a fallback.

## 11. Backup and restore

`docs/design/backup.md` is the spec and `docs/BACKUP-SETUP.md` the admin walkthrough.

- A goroutine in the server (`scheduler.go`) runs the backup nightly at `schedule_hour`, and at boot if the last success is stale. There is no OS scheduler. An advisory lock makes a second concurrent run a skip.
- One run writes every table as CSV from a single repeatable-read snapshot, plus sequences, readable `inventory.csv` and `accounts.csv`, a manifest with a SHA-256 per file, and `RESTORE.md`, zipped and optionally AES-256-GCM encrypted. It then pushes to each configured target: Google Drive through `rclone` (dated folders) and GitHub through its REST API (a fixed `backup/` path, history capped at `keep_days`). GitHub is labelled experimental. A failed push never fails the run, and it shows on the backup screen.
- Secret columns are redacted from the export, and a restore keeps the live secrets.
- Every restore goes through one `RestoreFromZip`: an uploaded zip, a date on Drive, a date on GitHub, or `stockroom restore`. It checks checksums before opening a transaction, loads with foreign keys suspended, then checks row counts, every foreign key and every sequence before committing. It writes sequences last, because `setval` is not transactional. It ends by clearing every session.
- `stockroom restore` (and `cmd/restore`) needs no session. It is the way back when the database holds no accounts.
- Admin → Export everything downloads the same archive, unencrypted, without needing a backup folder.
- Photos are mirrored to a local folder only, rolling to a frozen generation every `keep_days`. Nothing deletes them automatically. The backup screen warns on low disk space or too many generations.
- Staleness shows on the backup screen, at an admin's sign-in, and at every user's sign-in, naming admins to tell.
- The Drive connection is shared with the photo wall. Admin → Settings signs in to Google with one click, and a folder picker chooses the backup folder. A folder path must be absolute.

## 12. Open questions

Things that only a person, hardware or the school can settle. Each has a ROADMAP entry.

- The live webcam has not run through go2rtc on macOS or Linux, the school has not approved recording students, and the QuickCam and closet PC tuning are unmeasured (ROADMAP A1).
- No tag has been pushed, so no release exists and the Homebrew tap doesn't exist (ROADMAP §3, §6).
- The packages are proven in containers only. No VM or real machine has been rebooted to prove the service comes back after a power cut with nobody logged in, and macOS setup has never run (ROADMAP §5, §6).
- The barcode scanner isn't bought, so the scan threshold is untuned (ROADMAP §8).
- The closet PC's backup folders, photo-mirror disk and target credentials wait on the PC (ROADMAP §9).
- GitHub backup has never run against a real repository. Drive has.
- The school hasn't created its own Google OAuth client. Stockroom supports one (`docs/BACKUP-SETUP.md` step 3c), and its consent screen must be Published, because Testing expires refresh tokens after seven days.
- Whether the photo mirror gets a second physical disk (`docs/design/backup.md` §H).
- WSL is the last main-track section (ROADMAP §11). `get.ps1` and `dev.ps1` have never run on real Windows.

---

## Agent skills

### Issue tracker

Issues live in GitHub Issues for `Kathir-D/Stockroom` (via the `gh` CLI). See `docs/agents/issue-tracker.md`.

### Triage labels

Default vocabulary: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `CONTEXT.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.

## Code Exploration Rules
- The following rules apply if `codebase-memory-mcp` is installed:
ALWAYS query `codebase-memory-mcp` tools FIRST before using standard file tools (`grep`, `glob`, or reading individual files) to explore or analyze code structure.
- For finding symbols, function definitions, or references, use the `search_graph` tool instead of plain text search.
- For analyzing architectural context, call chains, or dependencies across files, use `trace_path` or `get_architecture`.
- Only read raw source files directly after narrowing down the specific targets through graph queries.
- If MCP memory tools are not available, use standard search tools (`grep`, directory listings, file reading) to analyze the codebase structure.

<!-- gitnexus:start -->
# GitNexus — Code Intelligence

This project is indexed by GitNexus as **Stockroom** (5063 symbols, 12432 relationships, 184 execution flows).

> Index stale? Run `node .gitnexus/run.cjs analyze --index-only` from the project root — it auto-selects an available runner. No `.gitnexus/run.cjs` yet? Bootstrap with `npx`, `bunx`, or `pnpm dlx` — e.g. `bunx gitnexus@latest analyze` (npm 11 npx crash; #1939).

## Always Do

- **MUST run impact before editing.** Use `impact({target: "symbolName", direction: "upstream"})` or `node .gitnexus/run.cjs impact "symbolName" --direction upstream --repo .`; report callers, processes, and risk. Never substitute grep for graph analysis. For unified PDG impact, add `mode: "pdg"` with optional `line: <N>` — it returns statement-level `affectedStatements` over CDG + REACHING_DEF and inter-procedural symbols in `interproceduralByDepth`/`byDepth`; no-layer/degraded PDG results are UNKNOWN-risk notes (`--pdg` layer). CLI equivalent: `node .gitnexus/run.cjs impact "symbolName" --direction upstream --mode pdg --line <N> --repo .`.
- **MUST analyze graph changes before committing.** Use `detect_changes({scope: "all"})` (MCP) or `node .gitnexus/run.cjs detect-changes --scope all --repo .` (CLI fallback). `partial: true` or `truncated: true` is not a clean check — a zero means unseen, not unaffected; re-run it. For regression review: `detect_changes({scope: "compare", base_ref: "main"})` or `node .gitnexus/run.cjs detect-changes --scope compare --base-ref "main" --repo .`.
- MUST warn on HIGH/CRITICAL `risk` pre-edit; never use `riskSharedAxes` to waive a HIGH/CRITICAL `risk` warning. Compare File/symbol: MCP File omits axes; Graph-RAG expands File.
- **MUST treat `risk: UNKNOWN` as unresolved, not as low.** An empty caller set is not evidence the symbol is unused — it can also mean the callers are not resolvable by the index (plain-object property access, dynamic dispatch, cross-language calls). `impact` pairs `UNKNOWN` with a `riskNote` saying so. Confirm with a text search before treating the symbol as safe to change or delete; do not proceed on the strength of a zero.
- **MUST use `query({search_query: "concept"})` for concepts/flows, `context({name: "symbolName"})` for a named symbol, or `impact` for blast radius, on read-only callers, dependencies, imports, or execution flow.** Graph first; text search only for empty/`UNKNOWN`/literals.
- For security review, `explain({target: "fileOrSymbol"})` lists taint findings (source→sink flows; needs `analyze --pdg`).
- For control/data dependence, `pdg_query({mode: "controls", target: "fileOrSymbol"})` answers "under what condition does X run?" (CDG, incl. guard clauses) and `pdg_query({mode: "flows", target, variable})` traces "where does variable Y flow?" (REACHING_DEF). `--pdg` layer.

## Never Do

- NEVER edit a function, class, or method before MCP/CLI impact analysis.
- NEVER ignore HIGH or CRITICAL risk warnings from impact analysis, and never read `UNKNOWN` as an all-clear — it means the walk could not answer, which is the one verdict that requires confirming by other means.
- NEVER rename symbols with find-and-replace — use `rename` which understands the call graph.
- NEVER commit before MCP/CLI graph change analysis.

## Resources

| Resource | Use for |
| --- | --- |
| `gitnexus://repo/Stockroom/context` | Codebase overview, check index freshness |
| `gitnexus://repo/Stockroom/clusters` | All functional areas |
| `gitnexus://repo/Stockroom/processes` | All execution flows |
| `gitnexus://repo/Stockroom/process/{name}` | Step-by-step execution trace |

## CLI

| Task | Read this skill file |
| --- | --- |
| Understand architecture / "How does X work?" | `.claude/skills/gitnexus-exploring/SKILL.md` |
| Blast radius / "What breaks if I change X?" | `.claude/skills/gitnexus-impact-analysis/SKILL.md` |
| Trace bugs / "Why is X failing?" | `.claude/skills/gitnexus-debugging/SKILL.md` |
| Rename / extract / split / refactor | `.claude/skills/gitnexus-refactoring/SKILL.md` |
| Tools, resources, schema reference | `.claude/skills/gitnexus-guide/SKILL.md` |
| Index, status, clean, wiki CLI commands | `.claude/skills/gitnexus-cli/SKILL.md` |

<!-- gitnexus:end -->
