# Stockroom: media department asset checkout system

This file is the master reference for the project: what it is, how it's built, how to set it up, and the build timeline. Keep it updated as decisions get made. It's meant to be the single source of truth for anyone (human or AI) picking up this codebase. `ROADMAP.md` tracks open work; this file explains the *why* and the *shape*.

Last major revision: 2026-09-04 (product flow, auth model, and backend architecture pinned down; Section 13). Amended 2026-09-13: browse-list ordering, custodian-field visibility, and the backend deepening pass (one `DB` handle, one category tree, endpoint table in Section 8; Section 13). Amended 2026-09-14: the frontend, built as the single `packages/ui` workspace package both hosts render (Sections 4, 5, 8, 9, 13). Amended 2026-09-14: the Phase 7 backup & restore design, specified in `docs/design/backup.md` (Sections 8, 9, 11, 13), and a pre-commit hook that runs the suite. Amended 2026-09-15: the four shell/PowerShell scripts consolidated into one `scripts/dev.sh` (+ `dev.ps1`) with subcommands (Sections 8, 9, 13). Amended 2026-09-18: Phase 8, kits — the last item on the build list — built and shipped (Sections 2, 6.2, 7, 8, 8.1, 12, 13; `docs/adr/0002`), and the backup system backtested end to end against a live database, which found and fixed three reporting defects (Section 13). Amended 2026-09-22: **the production install path** — a plain `postgres:17` container instead of the Supabase stack, the migrations and the web UI compiled into one binary that applies its own schema at boot, and an OS service that restarts it (Sections 3, 4, 5, 8, 8.1, 9, 13; `docs/INSTALL.md`). **Development is unchanged**: the Supabase CLI is still the dev database and `scripts/dev.sh` is still the dev loop. Amended 2026-09-22: **Phase B, the first-run experience** — a setup wizard, barcode labels and ID cards, the category/asset imports and bulk add, a configurable student-number format, and `examples/` (Sections 6.2, 8, 8.1, 13). Amended 2026-09-23: **Phase C, the parts of "publish and prove" that code can do**: an Export everything download, the GitHub target labelled experimental, a Windows CI job, the student and admin guides, and a secret scan of the history (Sections 8, 8.1, 13). Amended 2026-09-23: the docs rewritten for a public repository, `TODO.md` and `TEMPLATE-TODO.md` (both complete apart from open items) replaced by `ROADMAP.md`, and the photo wall redesign and closet camera planned there (Section 2). Amended 2026-09-24: **the photo wall is turned on by signing in to Google** from Admin → Photo wall, which creates a read-only rclone remote and starts the wall with no restart; `SIGNIN_PHOTOS_REMOTE` is now only the remote's name (Sections 8, 8.1, 9, 13).

---

## 1. What this is

Stockroom is a fully local equipment checkout/check-in system for the school's media department. It replaces manual tracking with barcode-driven scanning and custody tracking, all running on a single dedicated machine with no internet dependency for daily use.

### Target user flow

1. **Sign in by scanning a student ID card.** The card barcode encodes the student's 6-digit number. A scan signs them in instantly, no password. If the same number is *typed* by hand instead, a password is required.
2. **Browse a filterable list of equipment**. Filters (Type → Category → Subcategory/Model, plus free-text search) on the left, matching items on the right.
3. **Click an item → detail popup → "Add to Cart".** The cart is a pending set held by the frontend; nothing is checked out yet.
4. **Check out the cart.** The user picks a due date (max 7 days out); every item in the cart transitions to `checked_out` together, in one transaction. After checkout the UI offers a sign-out prompt.
5. **Every physical item has a barcode sticker encoding its serial number.** Scanning an item (while signed in) is the single "track it" action:
   - item is currently `checked_out` → **checked in immediately** (any signed-in user can return any item; optional damage note)
   - item is `available` → opens the same detail popup / add-to-cart path as clicking it
6. **Admin panel** (admin accounts only): manage assets, the category tree, users (incl. roster CSV import and password resets), view overdue items, run a backup.

---

## 2. Goals / Non-goals

**Goals**
- Know what equipment exists and who has it
- Check items in/out in seconds via barcode scan
- Full custody history on every asset and every user
- Overdue tracking surfaced in-app (admin list + warning at the user's next sign-in; overdue users are blocked from new checkouts until returned, admin can override)
- Zero dependency on internet for core daily operation
- Runs on both the Windows closet PC and a macOS dev machine
- Automated nightly off-site backup (CSV pushed to Google Drive via `rclone`, to GitHub via its REST API, or both; §11)

**Non-goals** (explicitly out of scope; tables may exist in the schema but nothing is built on them)
- Reservations / future bookings / double-booking prevention (maybe much later)
- Physical locations (building/room/shelf). Location is "who has it"
- Free-form tags, saved filter presets, per-asset custom fields
- Multi-station / LAN access. The web app is localhost-only on the closet PC
- Native mobile apps
- Email/SMS reminders (needs internet)
- GPS tagging

**Built last, as Phase 8 (2026-09-18).** Kits, a named bundle of assets (e.g. "Kit #1 = this camera + this lens + this bag") added to the cart and returned as one unit. A kit is a *label over assets* and holds no custody of its own: adding one to the cart expands it into its asset ids and `CheckOutAssets` commits them unchanged, one custody event per asset, so the 7-day cap, the overdue block and the custodian rule cannot differ between a kit and a handful of units. An asset belongs to at most one kit (`docs/adr/0002`). See §13 (2026-09-18).

**Planned next (`ROADMAP.md`, 2026-09-23).** Two features widen the scope above. The sign-in photo wall becomes a film strip over a set of about 150 photos from the student media Drive folder, held in memory rather than on disk. And a closet camera: a USB webcam, an existing open-source person detector (Frigate on Linux, Agent DVR as the Windows fallback), a local recording of every visit, and one timestamped, admin-only activity log of every walk-in, walk-out, sign-in, scan, checkout and check-in, so a missing item can be traced. Neither is built.

---

## 3. Deployment model

One dedicated PC lives in the camera closet, always on. Development happens on macOS.

**The development stack and the installed stack are deliberately different**, and since 2026-09-22 they are different in one specific way: development runs the Supabase CLI's whole Docker stack, and an install runs **one plain Postgres container and one binary** (`deploy/docker-compose.yml`, `docs/INSTALL.md`). The decisive reason is not the container count — the CLI ships Kong, GoTrue, PostgREST, Realtime, Storage and Studio, all of which Stockroom has never used (§4) — it is that shipping it puts **Studio on the closet PC, on port 54323, with no authentication**: full read and write on every table, the roster included, for anyone who walks past and opens a browser.

**On a developer's machine:**

1. **Supabase local stack** (Docker). Used purely as the Postgres host (+ Studio for poking at data), plus migrations, `seed.sql` and the pgTAP suite. Its REST API and Auth services are unused.
2. **Go HTTP server** (`server/`), run from the repo. The only process that talks to the database.
3. **Frontends**, each on its own Vite/Wails origin: the **Wails desktop app** (`desktop-app/`) and the **web app** (`web-app/`) on `localhost:5173`. Both render `<StockroomApp>` out of `packages/ui` and speak HTTP to the Go server.

**On the machine it is installed on:**

1. **One `postgres:17` container** (`deploy/docker-compose.yml`), bound to `127.0.0.1:54322`, with a password generated at install time.
2. **One binary** — the API, the web UI and every migration, compiled together. It applies whatever migrations are pending at boot, and serves the UI at `/` from the same origin as the API, so CORS never comes into it and `SERVER_ADDR` stops being load-bearing.
3. **A service** — a launchd agent on macOS, a systemd unit on Linux — that starts it and restarts it if it dies. Not optional: the nightly backup is a goroutine inside the server, so "the server is running" and "backups happen" are the same fact. The Wails desktop app stays available as the nicer local window, not as a requirement.

Windows is not covered by the installer yet (`ROADMAP.md`).

The USB barcode scanner plugs into this machine. Nightly, a goroutine inside the Go server exports every table to CSV into a local folder and pushes it to whichever off-site targets are configured: `rclone copy` to Google Drive, the REST API to GitHub, or both. `rclone` owns the Drive OAuth token and refresh handling (set up once from the admin panel, which drives `rclone authorize`); the Go code never talks to the Drive API directly. Specified, not yet built — see §11.

---

## 4. Backend architecture (decided): single Go backend over Supabase-hosted Postgres

**The Go server is the only database client.** `internal/stockroom` holds every query, transaction, permission check, and account operation. `server/` wraps it in HTTP handlers. Both frontends will call those endpoints with `fetch` through a small `lib/api.ts` wrapper; no Supabase JS client, no PostgREST, no database credentials in TypeScript.

**Current state (2026-09-14, Phase 6 landed).** Both frontends now call the Go server and nothing else. The data layer, every component and every screen live once, in the npm workspace package **`packages/ui` (`@stockroom/ui`)**; `desktop-app/frontend/src/App.svelte` and `web-app/src/App.svelte` each render `<StockroomApp>` and are otherwise empty. The supabase-js path is deleted. `docs/design/design-system.md` is the reference for the UI itself.

Why this shape:
- One place for all logic, written in Go (preferred over TS for this codebase).
- Both UIs share one code path and one session store, so behaviour can't drift.
- Postgres still runs inside the Supabase CLI stack because migrations, seed loading, and Studio are already set up and working. Go connects to the **direct Postgres port (54322)** via `DATABASE_URL`.

**Historical note.** An earlier iteration had the Svelte frontend calling PostgREST directly with the `service_role` key (`supabase/migrations/20260826180000_grant_service_role.sql`, `desktop-app/frontend/src/lib/supabase.ts` and `db.ts`). The migration stays; the TS files were deleted in Phase 6. RLS is off and stays off: only the Go server, connecting as `postgres`, reaches the DB.

---

## 5. Tech stack summary

| Layer | Technology |
|---|---|
| Database | PostgreSQL 17. **Development**: the Supabase local stack (Docker), with migrations, `seed.sql` and pgTAP via the CLI. **Installed**: one `postgres:17` container (`deploy/docker-compose.yml`), migrations applied by the server itself at boot |
| DB access | Go, `pgx/v5` + `pgxpool`, direct connection on port 54322 |
| Backend / API | Go `net/http` server (`server/`) on localhost, JSON endpoints; logic in `internal/stockroom` |
| Auth | Scan login (student number, no password) or typed login (student number + bcrypt password); in-memory session map in the Go server; `is_admin` flag gates the admin panel |
| Files | Profile + asset photos copied into a local `uploads/` dir, served by the Go server at `/files/…` |
| UI | `packages/ui` (`@stockroom/ui`), one npm-workspace package: shadcn-svelte v1 over Bits UI, the design tokens, every Stockroom component, every screen, the API client and the scanner. Ships raw `.svelte`; each app's Vite compiles it |
| Desktop app | Wails (Go window host + Svelte 5 + TypeScript frontend, Tailwind CSS v4). Renders `<StockroomApp>`; calls the Go server over HTTP |
| Web app | Vite + Svelte 5 + TypeScript, Tailwind CSS v4. Renders the same `<StockroomApp>`; localhost only |
| Barcode scanner | Standard USB HID keyboard-wedge scanner. Not yet tested with real hardware |
| Backup | A scheduler goroutine in the Go server → CSV per table + sequences + manifest + zip → local folder → `rclone copy` to Google Drive and/or the GitHub REST API. No OS scheduler, no `cmd/backup`. Restore is an admin-panel feature over one `RestoreFromZip`, with `cmd/restore` as the session-free floor (§11) |
| Schema | `supabase/migrations/*.sql`, read by **two** things: the CLI in development, and `stockroom.Migrate` at server start-up, which embeds the same directory (`supabase/embed.go`) and records into the same `supabase_migrations.schema_migrations` table |
| Install | `scripts/install.sh` (macOS, Linux). Builds the binary, generates `.env` with a fresh database password, starts Postgres, registers the service. Re-running it is the upgrade, and it dumps the database first. `docs/INSTALL.md` |
| Config | `.env` at repo root (Section 9), loaded into `stockroom.Config`; `Open` takes the parts the package needs as `Options`. Backup configuration is the exception: it lives in the `app_settings` row, with `.env` only seeding it on first boot |

---

## 6. Database schema

### 6.1 Base schema (applied)

`supabase/migrations/20260826173006_init_schema.sql` is the source; read it rather than a copy here. In short: 12 tables (`profiles`, `locations`, `categories`, `tags`, `assets`, `asset_tags`, `kits`, `kit_items`, `bookings`, `custody_events`, `activity_log`, `saved_filters`), 2 views (`active_custody`, `overdue_custody`: open custody rows, and the subset past `due_at`), 4 enums (`user_role`, `asset_status`, `booking_status`, `location_type`). Two triggers: `assets.updated_at` is stamped on update, and a status change writes an `activity_log` row. `bookings` carries a GiST exclusion constraint against overlapping reservations; nothing in v1 writes to it. Followed by `20260826180000_grant_service_role.sql` (historical, Section 4), `20260908100000_v1_flow.sql` and `20260913090000_category_sort_order.sql` (Section 6.2). `supabase/seed.sql` loads on `supabase db reset`.

### 6.2 Schema changes for v1 (applied, `20260908100000_v1_flow.sql`)

The base schema was designed for a broader feature set than v1 ships. One additive migration brings it in line with the flow in Section 1; nothing is dropped.

**`profiles`**
- `student_number text unique`. The 6-digit number encoded on the student ID barcode; the scan/typed login key
- `first_name text`, `last_name text`. Replaces reliance on `full_name` (kept, can be derived)
- `photo_path text`. Relative path under `uploads/`
- `is_admin boolean not null default false`. The *only* permission flag (Section 7). The `role user_role` column stays but is unused.
- `email` becomes nullable (roster CSV doesn't carry it)
- `password_hash` is now actually used: bcrypt hash, **null until the user sets one** (see Section 7)

**`assets`**
- `create unique index on assets(serial_number)`. The serial is the scan key. Barcode stickers encode it. Linear items (batteries, bags, SD cards) use model-prefixed serials like `T7B-001`, `T5B-001`, `SD-014`. **Keep serials short**: the small label (for batteries, SD cards and lens barrels) fits about 9 characters on a US Letter sheet and 7 on A4 before its bars get too thin to scan, so the old examples here, `T7IBAT-001` at 10, would not have printed on the one label they were meant for. `GET /labels/layouts` reports each layout's `max_serial_chars`, and the print dialog warns past it.
- **`serial_number` is `not null` and `asset_tag` is generated** (`20260914120000_asset_tag_autogen.sql`). The two columns had been doing one job: `asset_tag` came from the base schema, written before the barcode flow existed, and `serial_number` then took the identifier role. Entering real inventory would have meant typing two unique codes per unit, one of which nothing ever reads. Now `asset_tag` defaults to `'AST-' || lpad(nextval('assets_asset_tag_seq'), 6, '0')` — **in the column default, not in Go**, so the admin panel, `seed.sql`, a CSV import and a hand-written `INSERT` in Studio all get one without knowing they have to. `AssetInput` carries no `asset_tag`, `UpdateAsset` never touches it, and the detail dialog shows it to admins only. The serial is the one identifier anybody types or reads.
- `photo_path text`
- `asset_status` enum gains `'unavailable'`. The catch-all for broken/missing/retired. v1 uses only `available` / `checked_out` / `unavailable`; the other enum values are left in place, unused.

**`categories`.** One addition, `sort_order integer not null default 0` (`20260913090000_category_sort_order.sql`). It is a row's position among its *siblings*, ascending, with the name breaking a tie, and it exists because the browse screen sorts by `examples/categories.media-department.md`'s document order rather than alphabetically (§13, 2026-09-12) and nothing in the table recorded that order. Gaps and duplicates are harmless; the numbers never have to restart at 1 under a parent. Phase 5's category CRUD maintains it. Otherwise unchanged: used as a strict 3-level tree via `parent_id`:
`Type` (e.g. Lenses) → `Category` (e.g. Zooms) → `Subcategory / Model` (e.g. Canon 70-200mm f/2.8). Each physical unit is an `asset` whose `category_id` points at a Model node. Seeded from `examples/categories.media-department.md`, whose Type and Model names are used verbatim. That file names the Categories under `Lenses` (Zooms, Primes, Accessories) and `Cameras/Bodies` (Camera Model) but lists models straight under the other six types, so the seed invents a middle Category there (Lights → Studio Lights + Light Modifiers, Audio Stuff → Wireless Mics + Wired Mics, and so on); those six are the seed's own naming and are safe to rename. Branches may stop short of depth 3 when a Category has no models yet: `Primes` is seeded empty because `examples/categories.media-department.md` records none in inventory. `categories.name` is unique across the whole table, not per parent, so generic names are worth avoiding.

**`app_settings`, Phase B (`20260922100000`, `20260922110000`).** `student_number_format` (`digits` \| `alphanumeric` \| `custom`, default `digits`) and `student_number_pattern` (a Go regexp, used only for `custom`, anchored by the server) decide what a student number may look like; the sign-in field filters to the same rule via `GET /signin/config`. A save that existing accounts would fail is refused and names them. `setup_step` and `setup_completed_at` are the wizard's resume point and its done-marker; the migration marks any database that already has accounts as done, so an upgrade never sends a working install through the wizard.

**Unused in v1 (tables kept, no code written against them).** `locations`, `tags`, `asset_tags`, `bookings`, `saved_filters`, `assets.custom_fields`, `assets.location_id`.

**`kits` / `kit_items` (in use since Phase 8, `20260918090000_kits_v1.sql`).** No columns added; two unique indexes, both rules that must not be able to drift. `kits (lower(name))` because a kit name is a label on a bag and two kits cannot share one, case ignored. `kit_items (asset_id)` because an asset belongs to at most one kit: a unit shared between two bundles makes the second one incomplete without saying so, and unchecked that surfaces at the shelf, to a student who cannot fix it, rather than at kit-building time, to the admin who can (`docs/adr/0002`). `AddAssetToKit` inserts and reads the offending row back, so the refusal names the kit that already holds the unit.

---

## 7. Accounts & permissions

Two kinds of account, decided by `profiles.is_admin`:

| Capability | Non-admin (student) | Admin |
|---|---|---|
| Sign in by scan / by typed number + password | ✓ | ✓ |
| Browse, filter, view item details | ✓ | ✓ |
| Add to cart, check out **to self** | ✓ | ✓ |
| Check out **on behalf of someone else** (custodian picked from user list) | | ✓ |
| Scan an item to check it back in | ✓ (any item, not just own) | ✓ |
| See who currently holds a checked-out item | ✓ (any item, not just own) | ✓ |
| View own custody history | ✓ | ✓ |
| View any item's or user's full custody history | | ✓ |
| Admin panel: the active-custody and overdue lists (who has what, across everyone) | | ✓ |
| Admin panel: asset CRUD, mark unavailable, category tree CRUD | | ✓ |
| Admin panel: user CRUD, roster CSV import, set/reset any password | | ✓ |
| Admin panel: overdue list, override overdue-block on checkout, Backup Now | | ✓ |
| See the kits and what is in them; add a kit to the cart; return a whole kit | ✓ | ✓ |
| Build a kit: create, rename, delete, put units in and take them out | | ✓ |

**Custodian visibility.** Who currently holds a checked-out item is visible to any signed-in user. Deliberate, decided 2026-09-12: a student being able to find who has the lens they want outweighs withholding it, and there's no separate school privacy officer for this project to seek sign-off from. This applies only to the *current* holder: `GetAsset`, `ListAssets`, and `ScanItem` include it for every actor, so a browse row can say who has the lens without opening anything. What "who" means is the custodian's **name**, their due date and whether they are overdue, not their student number, which is the scan-login key and so stays admin-only (`AssetCustody.forViewer`, 2026-09-13). Past custodians (the full trail) stay admin-only via `GetAssetHistory`; a non-admin's own history is available only through `GetUserHistory`. Enforce this in the Go API, not the UI. The response itself omits history custodian identities for a non-admin actor, since a hidden field is still a `fetch` call away in the web app.

**Login rules**
- **Scan** (student number arrives as a fast keystroke burst + Enter): sign in with no password.
- **Typed** (same number entered by hand): password required, checked against `password_hash` with bcrypt.
- Roster-imported users have `password_hash = null` (a blank hash counts the same). Their first **scan** login prompts them to set a password before continuing; typed login is impossible until then. Creating a user in the admin panel leaves the account in that same state; the admin can set a password afterwards with the reset endpoint.
- Admins can set or reset any user's password from the admin panel. Passwords are 8 to 72 characters everywhere they are set.
- **Failsafe admin.** `.env` holds `ADMIN_STUDENT_NUMBER` + `ADMIN_PASSWORD`. On every server start, that account is ensured to exist with `is_admin = true` and that password. A way back into the admin panel that doesn't depend on any UI. It is never a startup requirement: unset, malformed, or rejected values are logged as warnings and the server starts without a failsafe admin, because a typo in `.env` must not take the whole API down.

**Sessions**
- In-memory session map (`SessionStore`, owned by `DB`). The login response returns the token and also sets it as an HttpOnly `stockroom_session` cookie; requests may send either `Authorization: Bearer <token>` or the cookie. Restarting the server signs everyone out; acceptable.
- Sessions persist until manual logout or an idle timeout (`SESSION_IDLE_MINUTES`, default **10 minutes**; 5 was decided 2026-09-12 and doubled 2026-09-14). The deadline is measured **from the last interaction, not from sign-in**: every request refreshes it server-side, and `packages/ui/src/lib/keep-alive.ts` turns real user interaction into a request when the connection has been quiet, because most of the browse screen costs no HTTP at all. After a checkout completes, the UI offers a "sign out?" prompt because the closet PC is shared.
- The frontend cart (a pending list of asset IDs, never sent to the server until checkout) clears only on sign-out or on the idle timeout, **not** on a page reload, so an accidental refresh mid-shopping doesn't lose it.
- A scan login by an account with no password gets a **limited** session: it may only call `POST /auth/set-password`, `GET /me` and `POST /auth/logout`. Anything else answers `403 {"error":"password not set","needs_password":true}`. Setting the password upgrades the same token to a full session.
- The actor's profile is reloaded on every request, so an admin-flag change or a deleted account takes effect immediately. An admin password reset or delete drops that user's sessions, and `internal/stockroom` does that itself so the rule does not depend on the HTTP layer.

**Overdue rule**
- Signing in with any overdue item shows a warning, and the cart/checkout UI disables itself immediately at that point. Attempting a checkout while overdue is *also* refused by the server (`CheckOutAssets` → `ErrOverdueBlocked`) regardless of what the client sends. Both layers enforce the block, not just the UI. An admin can override per checkout.

---

## 8. Repository structure

```
stockroom/
├── go.mod                     # single root module; desktop-app, server, cmd share internal/
├── .env.example               # copy to .env, see Section 9
├── internal/stockroom/        # ALL business logic; the only code that touches Postgres
│   ├── doc.go                 # package comment
│   ├── db.go                  # DB: the pool, the session store, UploadsDir, BackupDir. Open(ctx, url, Options)
│   ├── config.go              # .env + environment -> Config
│   ├── types.go               # row structs for every table + views
│   ├── errors.go              # sentinels server/ maps to statuses (ErrNotFound, ErrForbidden, ErrNotConfigured, ...)
│   ├── pgerr.go               # Postgres error codes -> ErrConflict / ErrInvalid / ErrNotFound
│   ├── password.go            # bcrypt helpers, student-number validation
│   ├── student_number.go      # the student-number format: digits | alphanumeric | custom regex, installed at boot
│   ├── failsafe.go            # EnsureFailsafeAdmin, run on every server start
│   ├── envfile.go             # setEnvValues: the ONE writer of .env (temp file + rename), for the failsafe only
│   ├── setup.go               # the first-run wizard: first admin, resume state, failsafe, load/remove examples
│   ├── sessions.go            # in-memory SessionStore with idle timeout
│   ├── auth.go                # Actor, RequireAdmin, RequireFullSession, the login/logout/Resolve/Me methods on DB
│   ├── users.go, roster.go    # user CRUD + password reset; roster CSV import
│   ├── categories.go          # categoryTree (the one in-memory shape of the table) + GetCategoryTree
│   ├── categories_admin.go    # category create/update/delete, depth and cycle rules
│   ├── categories_import.go   # a whole tree from indented text/Markdown or type,category,model CSV; idempotent
│   ├── assets.go              # browse: ListAssets, GetAsset, current-holder reads, browse sort
│   ├── assets_admin.go        # asset create/update/delete/status/photo
│   ├── custody.go             # ScanItem, CheckOutAssets, CheckInAsset, the lists and histories
│   ├── kits.go                # kits: CRUD, membership, CheckInKit. A kit holds no custody
│   ├── assets_import.go       # asset CSV import: upsert by serial, per-row errors, names what an update replaced
│   ├── assets_bulk.go         # N numbered units of one model: preview, then write, continuing the series
│   ├── barcode.go             # Code 128 as a PNG, and the label layouts with their measured limits
│   ├── labels.go              # the label sheet and ID-card PDFs, bars drawn as vectors
│   ├── photos.go              # staged photo writes, FilesPrefix, photo URLs
│   ├── settings.go            # app_settings: load/save, .env as a first-boot seed, secrets redacted
│   ├── backup.go              # the nightly run: snapshot, CSVs, inventory/accounts, prune, push
│   ├── export.go              # takeSnapshot (the one archive builder) + ExportEverything, the download
│   ├── archive.go             # manifest (SHA-256 per file + sequences), zip, optional AES-256-GCM
│   ├── restore.go             # the ONE RestoreFromZip every entry point calls
│   ├── backup_status.go       # BackupStatus, .last-success.json, backup.log, the sign-in warning
│   ├── scheduler.go           # StartBackupScheduler: the in-process nightly + boot catch-up
│   ├── photos_backup.go       # the generational photo mirror (+ diskfree_{unix,windows}.go)
│   ├── target.go              # BackupTarget: Push/Versions/Fetch/Test
│   ├── target_drive.go        # Google Drive via the rclone binary
│   ├── target_github.go       # GitHub via the REST API, no git binary
│   ├── drive_authorize.go     # `rclone authorize drive` driven from the admin panel
│   ├── photowall.go           # the sign-in photo wall's reel: prefetch, hand out, delete after use
│   ├── photowall_drive.go     # its source: the Drive folder's manifest, and one `rclone cat`
│   ├── photowall_image.go     # its normalizer: EXIF, ratio gate, crop, resize to one 900x600 JPEG
│   ├── photowall_admin.go    # its admin surface: link parsing, the Drive probe, the folder switch
│   ├── photowall_google.go   # its switch: Sign in with Google -> a drive.readonly remote -> the wall starts, no restart
│   ├── migrate.go             # applies pending migrations at boot; Supabase's own bookkeeping table
│   └── RESTORE.md             # embedded in every zip; the instructions travel with the backup
├── server/                    # net/http JSON API on localhost; handlers decode, call the package, encode
│   ├── main.go, router.go, json.go, session.go, files.go
│   ├── ui.go                  # serves the embedded web UI at / and /static/; the catch-all route
│   ├── imports.go             # the category/asset imports and bulk add; uploadBody takes multipart or a raw body
│   ├── labels.go              # label and ID-card PDFs, the barcode PNG, the layout list
│   ├── setup.go               # /signin/config and the /setup routes
│   └── auth.go, users.go, assets.go, custody.go, kits.go, admin.go, photowall.go
├── deploy/                    # what an INSTALL runs, as opposed to what development runs
│   ├── docker-compose.yml     # one postgres:17, 127.0.0.1 only, password from .env, no default
│   ├── stockroom-run.sh       # the one service entry point: wait for Docker, up the db, exec
│   ├── com.stockroom.server.plist.template   # launchd (an AGENT: Docker Desktop needs a session)
│   └── stockroom.service.template            # systemd (a system unit; Linux has no such problem)
├── cmd/restore/               # disaster CLI: calls the same RestoreFromZip as
│                              # the admin panel, passing stockroom.LocalCLIActor() -- so RequireAdmin still
│                              # holds, and it works with zero accounts in the database (§11)
│                              # NB: no cmd/backup. Phase 7 schedules the backup inside the server (§11)
├── uploads/                   # profile + asset photos (gitignored), served at /files/
├── package.json               # npm workspaces: packages/*, web-app, desktop-app/frontend
├── supabase/embed.go          # the migrations, as a Go package. ONE directory, two readers (§4)
├── web-app/embed.go           # the built UI, as a Go package. dist/.gitkeep is tracked
├── packages/ui/               # @stockroom/ui: ALL frontend code. Both hosts are five lines each
│   ├── components.json        # shadcn-svelte config; aliases are package-absolute, not $lib
│   └── src/lib/
│       ├── app.svelte         # the whole application: routing, the one scan listener, the 401 hook
│       ├── index.ts           # the package entry: <StockroomApp>, the api, the stores
│       ├── styles/tokens.css  # the only file allowed to define a colour, radius, shadow or duration
│       ├── api/               # client.ts (token, 401, errors) + index.ts (one function per endpoint)
│       ├── scanner.ts         # keystroke buffer, scan-vs-typed, the Ctrl+Shift+D diagnostic
│       ├── status.ts, due.ts  # the five-status system; the 7-day cap as an instant
│       ├── kits.ts            # expanding a kit into cart lines: whole or nothing
│       ├── stores/            # session, cart, cart-items, catalog, kits, scan, router (hash)
│       ├── components/ui/     # shadcn-svelte generated
│       ├── components/app/    # StatusDot, Serial, ModelRow, UnitRow, CartDock, ScanResult,
│       │                      # PasswordInput (masked field + reveal toggle), PhotoWall, FirstAdmin,
│       │                      # StudentNumberFormat, ImportDialog, BulkAddDialog, PrintLabelsDialog, BarcodeDialog,
│       │                      # GoogleConnectDialog (the rclone sign-in, shared by Settings and Photo wall), ...
│       └── screens/           # sign-in, setup (the wizard), browse, cart-page, kits, history, admin/{assets,categories,users,overdue,backup,settings,photo-wall}
├── desktop-app/               # Wails app, primary UI; Go side is only a window host
├── web-app/                   # Vite + Svelte 5 secondary UI
├── supabase/                  # config.toml, migrations/, seed.sql, tests/ (pgTAP)
├── .githooks/                 # pre-commit: runs `scripts/dev.sh test`; installed by `dev.sh deps` via core.hooksPath
├── scripts/                   # install.sh: the PRODUCTION install and upgrade (macOS, Linux)
│                              # dev.sh: the one DEVELOPMENT script (up|deps|test|stop|status). dev.ps1 is its Windows
│                              # counterpart (untested on Windows). Plus Start Stockroom.command (a
│                              # double-click wrapper for dev.sh up)
├── docs/                      # adr/ (decision records), agents/ (skill notes), design/ (design system, backup spec)
│                              # plus the guides: INSTALL, BACKUP-SETUP, ADMIN-GUIDE (the teacher's manual), STUDENT-GUIDE (one printable page)
├── examples/                  # obviously-fake example data ("Example …", numbers 900001-5, EXAMPLE- serials):
│                              # two category trees, a roster, an asset list. embed.go puts them in the binary
│                              # for the wizard's "load examples"; examples_test.go imports each one
├── CONTEXT.md                 # domain glossary
├── ROADMAP.md                 # open work only; completed work is in §13
├── CLAUDE.md, README.md, TESTING.md, CI.md
```

Neither app has a `src/lib/` any more: everything they used to hold moved into `packages/ui`, and `{supabase,db}.ts` are deleted.

### 8.1 Endpoints

Every route except `/health`, the two logins, `/signin/*`, `POST /setup/admin` and the static files needs a session. "Admin" below means `RequireAdmin` inside the package, not the router. A limited session (Section 7) reaches only the three routes marked so.

| Route | Who | Notes |
|---|---|---|
| `GET /health` | nobody | pings Postgres |
| `POST /auth/scan`, `POST /auth/password` | nobody | `{student_number}` / `{student_number, password}`; sets the cookie and returns the token |
| `POST /auth/set-password`, `POST /auth/logout`, `GET /me` | any, incl. limited | |
| `GET /categories/tree` | any full | nested `Type -> Category -> Model`, sibling order by `sort_order` |
| `GET /assets?category=&status=&q=`, `GET /assets/{id}` | any full | rows carry `category_path`, `photo_url`, `custody` (current holder) |
| `POST /scan` | any full | `{serial}`; out -> checked in, else -> detail. See Section 1 step 5 |
| `POST /checkout` | any full | `{asset_ids, due_at, custodian_id?, override_overdue?}`; the last two are admin-only |
| `POST /assets/{id}/checkin` | any full | optional `{note}` (the damage note; an empty body is fine) |
| `POST /custody/{id}/note` | any full | `{note}` onto a **closed** event's `condition_in`. A scan checks an item in before the "Add a note" surface renders, so the note has no check-in call left to ride; an open event is 409 |
| `GET /kits`, `GET /kits/{id}` | any full | the kit with its units as browse rows, plus `available`/`checked_out`/`unavailable` and `checkable` |
| `POST /kits`, `PUT/DELETE /kits/{id}` | admin | `{name, description?}`; a new kit is empty, and a delete removes the grouping only — no asset, status or custody row moves |
| `POST /kits/{id}/items`, `DELETE /kits/{id}/items/{assetId}` | admin | `{asset_id}`; a unit already in a kit is 409 **naming that kit**, a non-member is 404 |
| `POST /kits/{id}/checkin` | any full | returns every unit of the kit that is out. Per unit, never all-or-nothing: `returned`, `already_in`, `failed` |
| `GET /users/{id}/history` | own, or admin | |
| `GET /custody/active`, `GET /custody/overdue`, `GET /assets/{id}/history` | admin | |
| `GET/POST /users`, `GET/PUT/DELETE /users/{id}`, `POST /users/{id}/password` | admin | `UserInput` has no `photo_path`; the roster import is the only way a profile gets a photo |
| `POST /users/import` | admin | multipart `file` (+ optional `photo_dir`) or a `text/csv` body |
| `POST /assets`, `PUT/DELETE /assets/{id}`, `POST /assets/{id}/status` | admin | `AssetInput` has no `photo_path`, no status and no `asset_tag` (generated); `serial_number` is required. Status takes `{status: available\|unavailable}` |
| `POST /assets/{id}/photo` | admin | multipart `photo` part, 10 MB cap, `.jpg .jpeg .png .gif .webp` only; the file lands at `uploads/assets/<id>.<ext>` and the response is the asset with its new `photo_url` |
| `POST /users/cards.pdf` | admin | `{user_ids}`; a sheet of ID cards with a scannable barcode, for schools whose own cards carry none |
| `POST /categories/import` | admin | multipart `file` or a raw `text/plain`/`text/csv` body. Indented text, Markdown (headings and list items only) or `type,category,model` CSV, sniffed. One transaction with a savepoint per row, idempotent, so re-running a corrected file only adds what is new |
| `POST /assets/import` | admin | multipart `file` or a raw CSV body. Upserts by `serial_number`; per-row errors with line numbers; an update names what it replaced; status and custody are never touched |
| `POST /assets/bulk-preview`, `POST /assets/bulk` | admin | `{name, prefix, count, category_id?}`. The preview writes nothing and returns the exact serials; numbering continues from the highest already in use under that prefix |
| `GET /labels/layouts` | any full | the label sheets, named by what they go on, each with its paper and an approximate `max_serial_chars` |
| `POST /assets/labels.pdf` | admin | `{asset_ids, layout}`; a PDF to print at 100%. A serial too long for the layout is refused here, whatever the dialog warned |
| `GET /assets/{id}/barcode.png` | any full | `?width=`, `?download=1`. One barcode, big enough to scan off the monitor — the recovery path when a sticker falls off. Fetched by the UI rather than put in an `<img src>`, which cannot carry the token |
| `GET /setup`, `PUT /setup` | admin | the wizard's state: `{needs_admin, step, completed}`; PUT takes `{step, completed}` |
| `POST /setup/failsafe` | admin | `{student_number, password}`, written into the `.env` the server started from and ensured immediately. Refused if it is the caller's own number or an ordinary account's |
| `POST /setup/examples`, `DELETE /setup/examples` | admin | load `examples/` through the real importers, or remove exactly those rows. A half whose serials or numbers a real row already holds is skipped, not merged |
| `POST /categories`, `PUT/DELETE /categories/{id}` | admin | `{name, parent_id?, sort_order?}`; depth capped at 3, delete refused with children or assets |
| `POST /admin/backup` | admin | the whole run: archive, photo mirror, every enabled target. A failed push is in `targets`, not an error |
| `GET /admin/export` | admin | the backup's zip as a download (`stockroom-export-<date>.zip`), `no-store`. Needs no backup folder, takes no lock, is **never** encrypted, pushes nowhere. It restores through `POST /admin/restore` like any backup |
| `GET /admin/backup/status` | admin | the backup screen's one read: warnings (already worded), per-target state, photo generations, log tail |
| `GET /admin/backup/versions?target=` | admin | `local` / `drive` / `github`; `id` is opaque to the UI |
| `GET/PUT /admin/settings` | admin | secrets come back **blank** with a `_set` boolean, never masked; PUT is a partial update |
| `POST /admin/settings/test` | admin | `{target}`; runs that target's own connection check |
| `POST /admin/drive/connect`, `POST /admin/drive/finish` | admin | `rclone authorize drive` driven from the panel, so a revoked token never needs a terminal |
| `POST /admin/restore` | admin | multipart `file` + `confirm=RESTORE` (+ `passphrase`, `force`) |
| `POST /admin/restore/remote` | admin | the same restore, bytes fetched from a target by date |
| `GET /admin/photos/generations`, `POST /admin/photos/restore`, `DELETE /admin/photos/generations/{name}` | admin | the mirror. Deleting is the only deletion it has, and it is a person pressing a button |
| `GET /admin/photo-wall`, `PUT /admin/photo-wall`, `POST /admin/photo-wall/rebuild`, `GET /admin/photo-wall/preview` | admin | the sign-in photo wall's Drive folder. **No response ever carries the folder id or a Drive URL** — a share link is a capability, so the field is write-only and the screen gets a typed label, counts and a preview strip instead. `PUT` takes `{link, label}` and parses → probes Drive → writes → tears the reel down; a folder it cannot reach writes nothing and leaves the previous one live. The preview does not mark tiles served |
| `POST /admin/photo-wall/google/connect`, `POST /admin/photo-wall/google/finish` | admin | Sign in with Google for the wall: `rclone authorize` for a **`drive.readonly`** token, then `{id, code?}` writes the remote (`gdrive-photos` unless `SIGNIN_PHOTOS_REMOTE` renames it) and **starts the wall on the running server**. The token goes to rclone's own config and nowhere else; the status says `google_connected`. A sign-in started from the backup screen (full scope) is refused here with 409 |
| `GET /signin/config` | nobody | the sign-in field's filter for the student-number format, and `needs_setup` when there are no accounts yet. The sign-in screen works without it: digits stands in until it arrives |
| `POST /setup/admin` | nobody | `FirstAdminInput` (number, names, password, the student-number format). **Only while `profiles` is empty**, checked and inserted under a table lock, so two browsers racing the form make one admin |
| `GET /files/...` | nobody | photos off `UPLOADS_DIR`; `<img>` tags cannot send a bearer token |
| `GET /signin/photos` | nobody | the sign-in photo wall's batch: `{photos: [...], ttl_seconds}`. **Always 200**, with `[]` whenever the wall is off, unconfigured, warming up or drained with nothing left worth repeating — every one of those means "draw no columns" to the only caller, and the sign-in screen must never look broken (`docs/design/signin-photo-wall.html` §5, §9). A reel short of fresh tiles makes the batch up with repeats that keep their expiry, and a ceiling of twice the buffer on tiles on disk stops a caller in a loop from driving Drive downloads (§2, "Draining") |
| `GET /`, `GET /static/...` | nobody | the embedded web UI (`server/ui.go`, `web-app/embed.go`). `GET /` is the **least specific pattern on the mux**, so every route above still wins and the catch-all only ever sees paths no endpoint claims; a path that is not a file in `dist` answers `index.html`, which is what makes a bookmarked `#/kits` work. The bundle directory is `static`, **not** Vite's default `assets`, because `GET /assets/{id}` is already an endpoint and a default build would have the router answer a JavaScript request with "asset not found". A binary built with an empty `dist` answers 503 in words rather than a blank page |
| `GET /signin-photos/...` | nobody | the tiles, off `SIGNIN_PHOTOS_DIR/tiles/`. The **subdirectory**, never the cache root: the root holds `manifest.json`, which is a listing of the Drive folder, and `http.FileServer` serves any named file in a directory even though it refuses to list one |

**Statuses.** `ErrNotFound` 404, `ErrInvalid` 400, `ErrUnauthorized`/`ErrBadCredentials`/`ErrPasswordNotSet` 401, `ErrForbidden` 403, `ErrConflict`/`ErrOverdueBlocked` 409, anything else 500 with the detail logged, not sent. **`ErrNotConfigured` is 503** with its message intact: an unset `UPLOADS_DIR` or `BACKUP_DIR` is neither the client's fault nor a bug, and the admin reading the response is the person who edits `.env`.

---

## 9. Setup instructions

There are **two** paths now and they are not the same thing.

**Installing it on the machine it will live on** — `docs/INSTALL.md`, `./scripts/install.sh`. One
plain Postgres container, one binary, a service that restarts it. No Supabase CLI, no `seed.sql`,
no Studio; the database starts empty and the only account is the `.env` failsafe admin. Re-running
the installer is the upgrade, and it dumps the database before it touches anything.

**Setting up to work on it** — everything below, unchanged.

**Easiest path.** See `README.md`: `./scripts/dev.sh` (macOS) or `scripts\dev.ps1` (Windows) brings the whole environment up. Ctrl+C stops everything and preserves data. The same script carries every other working-copy command as a subcommand: `deps`, `test`, `stop`, `status`.

### Manual steps
1. Install Go, Node.js, Docker Desktop, the Supabase CLI, and the Wails CLI (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`).
2. Copy `.env.example` to `.env` and fill in:

   | Var | Purpose | Local default |
   |---|---|---|
   | `DATABASE_URL` | direct Postgres connection | `postgresql://postgres:postgres@127.0.0.1:54322/postgres` |
   | `SERVER_ADDR` | Go server listen address | `127.0.0.1:8080` |
   | `ADMIN_STUDENT_NUMBER` | failsafe admin account (Section 7); must fit the configured `student_number_format`, digits by default | (none) |
   | `ADMIN_PASSWORD` | failsafe admin password; 8 to 72 characters | (none) |
   | `UPLOADS_DIR` | where photos are copied | `./uploads` |
   | `BACKUP_DIR` | **first-boot seed** for the backup folder | (none) |
   | `PHOTO_BACKUP_DIR` | first-boot seed for the photo mirror folder | (none) |
   | `RCLONE_REMOTE` | first-boot seed for the Drive remote name | (none) |
   | `SESSION_IDLE_MINUTES` | idle timeout, from the last interaction | `10` |
   | `SIGNIN_PHOTOS_*` | the sign-in photo wall. It is **off until an admin presses Sign in with Google** in Admin → Photo wall (2026-09-24); `SIGNIN_PHOTOS_REMOTE` is only the rclone remote's name, blank meaning `gdrive-photos`. `SIGNIN_PHOTOS_FOLDER_ID` is a **first-boot seed** like the three above it — Admin → Photo wall owns the live folder afterwards | see `.env.example` |

   Those three backup variables are a **seed, not a setting** (Phase 7, `docs/design/backup.md` §C.2). `EnsureSettings` copies them into null `app_settings` columns on the first start against a fresh database and is ignored afterwards; from then on Admin → Settings owns them. Getting that direction backwards would mean a value an admin typed reverting on the next restart. `main.go` deliberately does *not* pass them through `Options`, so production always reads the row; the `DB.BackupDir` / `DB.PhotoBackupDir` fields exist as an explicit override for tests pointing at a temp directory.

   `docs/BACKUP-SETUP.md` is the click-by-click setup for a non-technical admin.

3. `supabase start` (repo root). Postgres + Studio (`http://127.0.0.1:54323`); migrations + seed apply automatically. The server would apply the same migrations itself if the CLI had not (`stockroom.Migrate`, into the same `supabase_migrations.schema_migrations` table), so this finds nothing to do and the two tools cannot disagree about what has run. The seed creates two development accounts, both with the typed-login password `password` and both able to sign in by scanning their number instead:

   | Student number | Name | Role |
   |---|---|---|
   | `123456` | Admin Admin | admin |
   | `234567` | Student Student | student |

   These are dev credentials for a localhost-only stack. The closet PC gets its real accounts from the roster import and the `.env` failsafe admin below, never from `seed.sql`. Note that neither seeded account is password-less any more, so the "set a password at first scan login" flow (§7) has no seeded example — create a user in the admin panel, which starts password-less, and scan their number to exercise it.
4. `go run ./server`. The API. Check `curl http://127.0.0.1:8080/health`.
5. `./scripts/dev.sh deps` (or `npm install` at the **repo root**, which is what it runs). It is an npm workspace: installing *inside* `web-app` or `desktop-app/frontend` creates a second copy of Svelte and Vite, and the failure is silent — components render, their state never updates. The script clears such a copy if it finds one and reinstalls from the lockfile; it also runs `go mod download`. `dev.sh up` and `dev.sh test` both call it, so there is one definition of an installed tree.
6. `cd desktop-app && wails dev`. Primary UI, native window.
7. `npm run dev:web` (or `cd web-app && npm run dev`). Secondary UI on `http://localhost:5173`.
8. `supabase stop`. Stops the stack, preserves data.

Same steps on Windows with PowerShell equivalents; the Go server and CLI are plain `go build` binaries on both OSes.

---

## 10. Barcode scanner notes

USB barcode scanners act as HID keyboard-wedge devices: they type the code followed by Enter into whatever has focus. No SDK or driver. Two kinds of barcode flow through the same keyboard buffer:

- **Student ID cards** → 6-digit number → sign-in
- **Item stickers** → serial number → `ScanItem` (check in, or open detail)

Which one a scan means is decided by **which screen is active**: on the sign-in screen a scan is a login; anywhere else it's an item scan. The backend never guesses. The frontend calls the matching endpoint.

**Scan vs typed detection** (frontend, `lib/scanner.ts`): a scanner emits keystrokes within a few ms of each other and ends with Enter. If the whole burst arrives under a threshold (~50 ms between keys, tune with real hardware), treat it as a scan → `LoginByScan`. Otherwise it's typed → show the password field → `LoginByPassword`. Keep a visible input focused so manual entry works identically as a fallback.

```js
let buffer = '', lastKey = 0, fast = true;
window.addEventListener('keydown', (e) => {
  const now = performance.now();
  if (buffer.length && now - lastKey > 50) fast = false;
  lastKey = now;
  if (e.key === 'Enter') {
    onCode(buffer, fast);   // fast === true → treat as scan
    buffer = ''; fast = true;
  } else if (e.key.length === 1) {
    buffer += e.key;
  }
});
```

Scanner hardware is not yet purchased/tested (Week 7). Confirm it's a plain HID keyboard-wedge model with no proprietary software.

---

## 11. Backup strategy

**Built (Phase 7, 2026-09-17).** `docs/design/backup.md` is the spec and the reasoning; `docs/BACKUP-SETUP.md` is the setup walkthrough for a non-technical admin. What follows is the summary.

**The nightly run** is a goroutine inside the Go server (`scheduler.go`), not Task Scheduler and not launchd. It fires at `schedule_hour` and runs immediately on boot when the last success is stale, which is the whole of "back up first thing when the machine is available". It also sidesteps `findDotEnv` walking up from a working directory that would have been `System32` under a Windows task. One run, guarded by `pg_try_advisory_lock` on a dedicated connection — `false` is a skip, not an error — writes: every table as CSV in one repeatable-read snapshot, `sequences.csv` carrying `last_value` **and** `is_called`, a readable `inventory.csv` and `accounts.csv`, a `manifest.json` with a SHA-256 per file and the schema version, and an embedded `RESTORE.md`, zipped and optionally encrypted.

**Two off-site targets, both active when both are configured.** Google Drive through the `rclone` binary and GitHub through its REST API (no `git` binary). Drive gets dated folders; GitHub overwrites a fixed `backup/` path so its commit history *is* the dated backup list and the repo grows by a few KB of text a night rather than a zip. GitHub's history is capped at `keep_days` and pruned by rewriting the branch onto a fresh orphan root, because overwriting `accounts.csv` does not remove its old versions and that file is a credential roster. A push that fails never fails the run: the restorable archive is already on disk, and each failure is carried into the state file and onto the backup screen rather than logged and forgotten.

**One restore, four entry points.** Upload a zip, pick a date on Drive, pick a date on GitHub, or `cmd/restore` — all of them call the same `RestoreFromZip`, and the GitHub fetch repackages its tarball into the same zip the local export writes. `set local session_replication_role = replica` is what makes a CSV reload work at all, and because it suspends foreign-key enforcement the restore re-checks everything inside the transaction *before* it commits: per-file checksums (before the transaction even opens, so a corrupt download costs nothing), row counts against the manifest, an anti-join per foreign key enumerated from `pg_constraint`, and every sequence's effective next value proven strictly above its column's maximum. **Sequences are written last, after every rollback-triggering check**, because `setval` is not transactional; the captured prior values are replayed, loudly, if the commit still fails. A restore clears every session as its last act.

`cmd/restore` is the guaranteed floor rather than the exotic path: `EnsureFailsafeAdmin` is best-effort by the 2026-09-08 decision, so an unfilled `.env` restores a wiped database into zero accounts and makes the admin panel unreachable exactly when it is needed. The CLI needs no session, **satisfies** `RequireAdmin` via `LocalCLIActor()` rather than bypassing it, and `BackupStatus` warns whenever no failsafe admin is configured.

**Configuration lives in the database** (`app_settings`), not `.env`, because no admin should have to edit a file. `.env` seeds it on first boot and is ignored afterwards. The export **redacts secret columns** — `app_settings` sits in `public`, so an unredacted export would push `github_token` to the very repo it unlocks and GitHub's secret scanning would revoke it — and the restore puts the live secrets back rather than overwriting them with the redacted nulls.

**Photos are mirrored locally only**, to one live folder updated in place, rolling to a frozen generation every `keep_days`. Never deleted automatically, so the mirror only grows; a free-space and generation-count alert bounds it and an admin deletes a named generation by hand. This is a deliberate trade: a failed drive loses `uploads/` and its mirror together.

**Nothing fails quietly.** `.last-success.json` per target and a rotating `backup.log` feed `BackupStatus`, and staleness is surfaced on three surfaces: the backup screen, an admin's sign-in, and **every** user's sign-in — naming the admins to tell, by name only, never a student number (§7).

**Verified.** A real truncate-and-reload round-trip against the live database, in the Go suite and by hand through the UI: 86 rows across 13 tables restored, a canary asset created after the backup correctly gone, and `assets_asset_tag_seq` resuming at its captured value (267) rather than 1, with no spurious `activity_log` rows.

## 12. Build timeline (9 weeks)

**Weeks 1 to 2 (done).** Environment prep, tooling, planning.

**Week 3 (done).** Chose Supabase-hosted Postgres. Schema migration written and applied; all tables/views/enums verified.

**Week 4 (done).** Supabase stack healthy; seed data; proved the chain end-to-end with a temporary supabase-js admin screen in the Wails app (asset + tag CRUD). Fixed two scaffold bugs: `vite@^8` → `^7` for `@sveltejs/vite-plugin-svelte@6`, and Svelte 5 needs `mount(App, …)` not `new App(…)` (blank window, no error). Wrote README + one-step start scripts (since consolidated into `scripts/dev.sh`, verified; `dev.ps1` untested). Interviewed and pinned down the product flow, auth model, and Go-backend architecture (this document).

**Week 5: Go foundation + schema + auth** (TODO Phases 0 to 2)
Root `go.mod`, `internal/stockroom` + `server/` skeleton, pgx connection, `.env`. v1 migration (Section 6.2) and category seed. Scan/typed login, sessions, `RequireAdmin`, failsafe admin, roster CSV import, user CRUD. Start scripts launch the server on both OSes.

**Week 6: Browse + core loop** (TODO Phases 3 to 4)
Asset list with category-tree filters, item detail, `ScanItem`, bulk cart checkout with 7-day due cap and overdue block, check-in with damage note, custody history, overdue list. **All core logic finalized by the end of this week.**

**Week 7: Admin panel + real data + scanner** (TODO Phase 5, hardware)
Admin panel endpoints (asset/category/user management, overdue, Backup Now). Buy the barcode scanner, tune scan-vs-typed detection against it. Print serial stickers; begin real inventory entry (recruit a CS class / volunteers).

**Week 8: UI + frontend wiring + backup** (TODO Phases 6 to 7)
Build the screens in the Wails app first, then mirror in the web app, both on `lib/api.ts`. Delete the supabase-js path. In-server backup scheduling + the two off-site targets + restore test.

**Week 9: Kits if time, then testing + presentation** (TODO Phase 8)
Kits **built 2026-09-18** (TODO Phase 8): the package, the eight routes, the Kits screen, and the seeded example kit. Final testing, walkthrough prep, presentation.

---

## 13. Decisions log

**Closed (2026-09-04 interview)**
- [x] Backend: **one Go backend** (`internal/stockroom` + `server/` HTTP) is the only DB client; both frontends use HTTP. Replaces the supabase-js/service_role approach.
- [x] Database host: **keep the Supabase Docker stack** for Postgres/migrations/Studio. Must run on Windows and macOS.
- [x] Frontends: **Svelte 5** in both; web-app stays **Vite + Svelte** (Astro dropped for good); web-app is **localhost only**.
- [x] Roles: **admin vs non-admin** boolean. The 4-tier `user_role` enum is unused.
- [x] Login: scan = no password; typed = password. Imported users set a password on first scan login. Admin can reset. `.env` failsafe admin.
- [x] Sessions: persist until logout/idle timeout; sign-out prompt after checkout.
- [x] Scan key for items: `assets.serial_number` (unique), on printed stickers; linear items use model-prefixed serials (`T7iBat-001`).
- [x] Category model: 3-level tree Type → Category → Model via `categories.parent_id`; asset = physical unit. Seed from `Catagories.md`. No Year filter.
- [x] Statuses in use: `available` / `checked_out` / `unavailable`.
- [x] Checkout: user-chosen due date, max 7 days. Admin picks custodian from user list. Overdue users blocked (admin override).
- [x] Check-in: requires sign-in; anyone can return any item; optional damage note.
- [x] Photos: profile + asset photos in local `uploads/`, served by Go.
- [x] Out of scope: bookings, locations, tags, saved filters, custom fields, LAN access, email. Kits = lowest priority. *Kits shipped 2026-09-18 as Phase 8; everything else here is still out of scope.*
- [x] Backup: nightly CSV via Go CLI into a **Google Drive** folder. *Superseded 2026-09-14 (Phase 7 design): Drive is one of two targets, the second being GitHub, and the nightly run is a goroutine inside the server rather than a CLI. See §11 and `docs/design/backup.md`.*
- [x] Styling (2026-09-05): **Tailwind CSS v4** in both frontends via `@tailwindcss/vite`; design tokens live in each app's `src/app.css` `@theme` block (desktop: the dark "Nocturne" system from the UI import). No component CSS files, no `tailwind.config.js`.

- [x] Failsafe admin is best-effort (2026-09-08): `EnsureFailsafeAdmin` returns `ErrFailsafeNotConfigured` when the `.env` values are blank, and the server only ever logs a warning. Nothing about the failsafe can stop the API from starting.
- [x] Student numbers (2026-09-08): stored as digits-only text, no fixed length. Cards encode six digits, but `NormalizeStudentNumber` accepts 1 to 32 so a reissued or imported number still works; the bound is a mis-scan guard, not a format.

**Closed (2026-09-12 grilling session; also resolves `docs/design/design-system.md` §15 Q1 to Q7)**
- [x] Session idle timeout: **5 minutes** (`SESSION_IDLE_MINUTES` default). *Superseded 2026-09-14: 10 minutes, measured from the last interaction.*
- [x] Machine operation model: **unattended student self-service** is the primary path; admin check-out-on-behalf-of stays the rare override it was already specced as.
- [x] Cart never mixes borrowing and returning: scanning a checked-out item checks it in immediately and never touches the cart; the cart only ever accumulates items being borrowed.
- [x] Scanning an item barcode with nobody signed in shows an explicit "sign in first" message rather than trying to interpret the code as a student number.
- [x] The frontend cart clears only on sign-out or the idle timeout, **not** on a page reload.
- [x] Overdue block is enforced at **both** layers: the checkout UI disables itself the moment an overdue user signs in, and `CheckOutAssets` also refuses server-side regardless of what the client sends.
- [x] **Custodian visibility, reversing the 2026-09-09 review tightening**: who currently holds a checked-out item is visible to any signed-in user, not admin-only. Applies only to the current holder. `GetAssetHistory`'s full past-custodian trail stays admin-only, and a non-admin's own history is available only via `GetUserHistory`. See §7.
- [x] Browse list sort order (previously unspecified): categories in `Catagories.md`'s document order, not alphabetical; within any list, available units sort before checked-out ones.
- [x] Backup: nightly CSV → local folder → **`rclone copy` pushes it to Google Drive** directly, replacing the "Drive desktop client syncs a local folder, no cloud API code" plan. One-time interactive `rclone config` OAuth setup instead of hand-written Google API/OAuth code. See §11. *Still current as far as Drive goes — `rclone copy` is exactly how the Drive target works. Phase 7 (2026-09-14) only widens it: Drive became one of two targets alongside GitHub, and the OAuth setup moved from a terminal into the admin panel via `rclone authorize`.*

**Closed (2026-09-12, building Phase 4)**
- [x] **The two custody *lists* are admin-only.** The 2026-09-12 decision opens the current holder of a *named* item to everyone (`ListAssets`, `GetAsset`, `ScanItem` name it for every actor). `ListActiveCustody` / `ListOverdueCustody` are the other thing: the roster of who has what, which §8.7 of the design doc specs as admin-panel screens. A student who wants to know who has the lens they want still finds it in the browse list.
- [x] **The 7-day cap is an exact instant**, `now + 7×24h` at the moment of the request, not "the end of the seventh day". A date picker that offers a *date* has to send an instant at or before that, and the `ErrInvalid` message names the instant so the UI can say why. Frontend work in Phase 6 has to respect it.
- [x] **A cart is a set.** The same asset id twice is one item, not a failed checkout.
- [x] **The open custody row, not `assets.status`, decides whether an item is out.** Check-in follows the row (matching what `GetAsset` already did for the current custodian), and checkout refuses an asset that claims to be available while a row is still open. Status drift becomes visible instead of duplicating custody rows.
- [x] **A limited session is refused inside `internal/stockroom`**, not only by the router: `RequireFullSession` sits beside `RequireAdmin` and guards every core-loop write.

**Closed (2026-09-13, Phase 3 addendum)**
- [x] **Document order is a column, not a constant.** `categories.sort_order` holds a row's position among its siblings; the seed fills it from `Catagories.md`. A hardcoded list of the eight Type names in Go was the alternative, and it breaks the moment Phase 5 lets an admin rename a Type.
- [x] **The browse list is sorted in Go, not in SQL.** Its first key is the asset's position in the category tree, which the single category read `ListAssets` already does; expressing it as an order-by would mean a recursive join per request for a list of a couple hundred rows.
- [x] **`AssetDetail` is an alias for `AssetListItem`.** Once every list row carries the current holder, the detail popup knows nothing a row doesn't. Two names, one struct, no drift between the scan payload and the click payload.
- [x] **A custodian's student number is admin-only**, narrowing the 2026-09-12 visibility decision by one field. The number signs its owner in by scan with no password, so a browse list carrying it is a roster of usable credentials. The name, which is what the decision was actually about, stays open to every signed-in user.

**Closed (2026-09-13, backend deepening)**
- [x] **One handle.** `Auth` is gone; `DB` owns the pool, the `SessionStore`, `UploadsDir` and `BackupDir`, built by `Open(ctx, url, Options)`. `server/` holds a `deps{db}` and nothing else. Two structs that each needed the other was one struct.
- [x] **One category tree.** `categoryIndex` (browse paths and sort keys) and `categoryTreeShape` (admin depth and cycle checks) were two readings of the same table; `categoryTree` in `categories.go` is the only one, and `requireCategory` and the browse filter read it too. The filter is `category_id = any(descendants)` from that tree, not a recursive CTE per request.
- [x] **`photo_path` has one writer per table.** `SetAssetPhoto` for assets, the roster import for profiles. `AssetInput` and `UserInput` lost their `photo_path`, because a form field that writes the column can name a file nobody uploaded or drop the pointer to one somebody did.
- [x] **An asset may file under any category node**, not only a Model. See `docs/adr/0001-assets-file-under-any-category-node.md`.
- [x] **`GetCategoryTree` takes the actor** and refuses a limited session inside the package, like every other read. Before this the router alone kept a password-less scan login off the tree.
- [x] **"Out" has one SQL definition**, `openCustodySQL` in `custody.go`, used by the scan branch, the cart lock, `DeleteAsset` and `SetAssetStatus`.
- [x] **Directories come from `DB`, not parameters.** `ImportRoster`, `SetAssetPhoto`, `BackupNow` and `ExportAllTablesToCSV` read `db.UploadsDir` / `db.BackupDir`; an empty one is `ErrNotConfigured`, which is a 503 (Section 8.1).
- [x] **The test suite was cut to one happy path and one gate per module** (30 Go tests, 3 pgTAP files), with the removed cases listed in `TESTING.md` under Planned. CI skips the build on docs-only changes while still reporting the `tests` check green (`CI.md`).

**Closed (2026-09-14, Phase 6: the frontend)**
- [x] **One package, not two apps.** Every component, screen, store and the API client live in `packages/ui`; both hosts render `<StockroomApp>` and hold nothing else. The design doc's §14 step 10 predicted the alternative ("if it turns into a rewrite, something leaked into `desktop-app/frontend/src/lib/`"), so the leak is prevented structurally rather than by discipline.
- [x] **The damage note needed a new endpoint.** `POST /custody/{id}/note` → `AnnotateCustodyEvent`. Scanning a checked-out item checks it in immediately (§1.5), so by the time the surface offers "Add a note" the check-in has committed and `CheckInAsset`'s note parameter is gone. The note lands on the same `condition_in` column, so a return's condition has one home however it was entered. An event still open is `ErrConflict`, not a silent no-op.
- [x] **CORS is an allow-list of loopback origins**, echoed back rather than starred, because the server also sets an HttpOnly cookie and the spec forbids pairing credentials with a wildcard. Not a step toward LAN access (§2).
- [x] **The sign-in field accepts a value it didn't see typed.** The scanner ignores an Enter with an empty keystroke buffer — correct for a barcode, but it left the form dead for a paste or an autofill, against §10's "manual entry works identically as a fallback". The native submit now picks those up, always as *typed* (a paste has no rhythm to measure), never as a scan.
- [x] **One toolchain.** `desktop-app/frontend` carried a stale nested `node_modules` (Vite 7, plugin-svelte 6, vitest 3) against the root's 8/7/5 — the exact "two Svelte copies, reactivity silently breaks" hazard design-system.md §2.2 names. Removed, `dedupe: ['svelte']` added to both Vite configs, and `svelte-preprocess` dropped for `vitePreprocess` (it rewrites `.svelte` files inside `node_modules` and breaks bits-ui's rune detection).
- [x] **`asset_tag` is internal and generated; `serial_number` is the identifier.** The base schema's `asset_tag` predated the barcode flow and had become a second unique code per unit that nobody reads — the admin form demanded it, and real inventory entry would have meant typing two. The generator is a **column default backed by a sequence**, not Go, so every writer gets a tag without knowing it must; `AssetInput` no longer accepts one and `UpdateAsset` never rewrites it. `serial_number` is `not null` in exchange, because an asset with no serial cannot be scanned and scanning is the only way an item comes back (§1.5). Migration `20260914120000`.
- [x] **One install, called from everywhere.** `scripts/dev.sh deps` is the single definition of an up-to-date tree — clear a shadowing nested `node_modules`, `npm install` (or `npm ci`) at the root, `go mod download` — and `dev.sh up` and `dev.sh test` both call it rather than each spelling it out. `dev.ps1` keeps a PowerShell copy. CI installs once at the root for the same reason: a per-app `npm ci --prefix` is the nested-install bug in the place nobody watches. A nested `node_modules` counts as *shadowing* only when it holds a real package; `.vite`, `.vite-temp` and `.bin` are normal and must not trigger a reinstall.

**Closed (2026-09-14, Phase 7 design; `docs/design/backup.md`)**
- [x] **Two targets, not one, and both at once when both are configured.** Google Drive via `rclone` and GitHub via its REST API. Drive won the "which is easier for a non-technical teacher to set up" question outright: handover is a Google sign-in screen they already recognise, where GitHub needs an account, a repository and a personal access token explained first. GitHub is kept because it needs no installed software at all and is the harder of the two for a school firewall to block.
- [x] **GitHub stores a fixed `backup/` path, overwritten nightly, one commit per run, over a bounded history.** Dated folders with a zip apiece would add a full incompressible blob every night with no way to prune without rewriting history. CSVs are text, so git deltas them to a few KB, and the commit log *becomes* the dated backup list the in-app picker reads. But overwriting a file does not remove its old versions — every prior `accounts.csv` stays reachable forever, and that file is a credential roster, so a deleted student is still a working scan-login in every commit predating the deletion. The history is therefore capped at `keep_days` and pruned by rewriting the branch onto a fresh orphan root, which is the only purge the REST API can actually perform. Documented with its two honest caveats: unreachable objects survive until GitHub garbage-collects them, so scrubbing a specific account means deleting and recreating the repository, and a force-update is the one irreversible write the app makes.
- [x] **The normal restore is an admin-panel feature, not a shell script, and `cmd/restore` is the guaranteed floor under it.** The ceiling on user effort is uploading one file, or picking a date. `psql` is not installed on the dev machine and Postgres runs inside Docker, so a `\copy`-based script had no client to run in anyway. The panel route depends on `EnsureFailsafeAdmin` having an account to recreate, and that is conditional: the failsafe is best-effort by the 2026-09-08 decision, which stays, so an unfilled `.env` restores a wiped database into zero accounts and makes the admin panel unreachable exactly when it is needed. Rather than make `.env` a startup requirement, `cmd/restore` is specified as session-free — its trust boundary is shell access to the closet PC, already more access than any account grants — and the backup screen warns when no failsafe admin is configured. Every entry point calls one `RestoreFromZip`; an emergency-only second implementation is one that has never been tested when it runs.
- [x] **One restore code path for three sources.** The GitHub fetch repackages its tarball into the same zip the local export writes, so upload, Drive-by-date and GitHub-by-date all funnel into `RestoreFromZip`.
- [x] **`set local session_replication_role = replica` is what makes a CSV reload work, and is why the restore has to validate before it commits.** It suspends foreign-key checks, so alphabetical file order stops mattering (`assets.csv` sorts before the `categories.csv` it references), and it silences `trg_asset_status_log`, so `activity_log` restores clean instead of gaining a junk row per asset. The cost is that nothing on the way in rejects an `assets` row pointing at a category that is not in the archive: unchecked, replica mode turns a clean failure into a permanently inconsistent database, which is worse than the ordering problem it solves. So inside the same transaction, before `Commit`, the restore resets `session_replication_role` to `origin` and runs an anti-join per foreign key enumerated from `pg_constraint` (not hand-listed, so a future migration's FK is covered for free), checks row counts against the manifest, and checks every sequence's *effective next value* — `last_value + increment_by` when `is_called`, `last_value` when not — sits strictly above its column's maximum, refusing a descending or cycling sequence outright rather than running a check never designed for one. The flag is captured because `pg_sequences` cannot supply it: a never-read sequence reports a null `last_value` there while the relation holds `is_called = false`, and replaying such a sequence with `setval`'s default of `true` silently burns its first value (verified: `setval('s', 5, true)` on a fresh `start 5` sequence makes `nextval` return 6). Any failure returns an error and the deferred rollback restores the tables exactly. **Sequences are written only after every check passes, because `setval` is not transactional** — verified: a `setval` inside an explicit `rollback` survives it. Written before the checks, a validation failure would roll the tables back while leaving every sequence advanced, producing a database that looks untouched and hands out colliding keys. The gap between the sequence writes and the commit is compensated by replaying the captured prior values, best-effort and loudly logged, since that compensation is non-transactional too. Per-file SHA-256 digests are verified earlier still, before the transaction opens, because a corrupted download should cost nothing — and because a CSV truncated mid-field is the one corruption a row count cannot see.
- [x] **Backup configuration moves into the database.** No admin or user edits a file after the one-time software install; `.env` seeds `app_settings` on first boot and stops being the source of truth.
- [x] **Secret columns are redacted from the export.** `app_settings` is in `public`, so `publicTables` would sweep `github_token` into `app_settings.csv` and push it to the repository it grants write access to; GitHub's secret scanning would then revoke it, killing backups silently hours after the first successful push.
- [x] **The scheduler is a goroutine in the Go server**, replacing Task Scheduler and launchd. On boot it runs immediately when the last success is stale, which is the whole of "back up first thing when the machine becomes available", and it sidesteps `findDotEnv` walking up from a working directory that would have been `System32` under a Windows task.
- [x] **Photos are mirrored locally only, one live folder, generational rollover, bounded by an alert rather than a purge.** Not a copy per day; the live folder is updated in place and freezes into a kept generation every `keep_days`. Never deletes, so an accidental delete stays recoverable. That also means it only grows, and since photos are mirrored in the same run that writes the database backup, an exhausted disk breaks *the backup* — a backup system whose failure mode is silently not backing up. `photo_min_free_gb` (default 5) and `photo_max_generations` (default 8, two years at the default `keep_days`) therefore warn on the same surfaces staleness uses, and the backup screen lists each generation with its size and a delete button. Deletion stays manual, because auto-purging the only copy of a deleted photo defeats the mirror. The bound is on photo generations only: local dated database folders are pruned automatically, being redundant with the off-site copies. The accepted cost remains that a dead drive loses `uploads/` and its mirror together.
- [x] **`accounts.csv` carries `password_hash`, and that is the most it can carry.** Plaintext passwords are not recoverable anywhere in the system: bcrypt is one-way and the server never stores what was typed. Restoring the hash is equivalent for the user, which is a statement about restore correctness and not a security claim — a bcrypt hash is offline-crackable at leisure by anyone holding the file, and the file leaves the machine. Dropping `password_hash` and forcing a post-restore reset was considered and rejected: the student number sitting beside it in the same file is a working password-free login (§7), so omitting the hash protects the weaker credential while leaving the archive just as sensitive and making every restore worse. The real options are to accept the archive as sensitive — which the owner did, knowingly, for a localhost-only school deployment where target access control is the control — or to encrypt all of it. `docs/design/backup.md` §C.5 specifies the latter as opt-in: AES-256-GCM under a scrypt-derived passphrase held outside both targets, trading a confidentiality risk for the availability risk that a lost passphrase is an unrecoverable backup.
- [x] **Student numbers leaving the machine was raised and accepted.** A student number is a working credential (§7), so the pushed folder is a roster of usable logins. It is why the GitHub repository must be private.
- [x] **The suite runs before a commit, not only in CI.** `.githooks/pre-commit` calls `scripts/dev.sh test`; `dev.sh deps` points `core.hooksPath` at the tracked directory so a fresh clone is protected without anyone running a `git config` line. Docs-only commits skip it on the same path list the workflow uses.

**Closed (2026-09-15, one script)**
- [x] **`scripts/dev.sh` is the only script**, with `up` (the default), `deps`, `test`, `stop` and `status` as subcommands; `scripts/dev.ps1` is its Windows counterpart and `Start Stockroom.command` a five-line double-click wrapper. It replaces `start-mac.sh` + `ensure-deps.sh` + `test-all.sh` + `start-windows.ps1`. The three bash scripts had already started to drift — the start script grew a health gate that the test script never got — and "the dependencies are up to date" meant something slightly different in each. The 2026-09-14 "one install, called from everywhere" decision survives intact: it is now one *function* rather than one file, called by `up` and by `test`, so the property that mattered (one definition, no copies) is unchanged while the number of entry points went from four to one. `dev.ps1` stays a separate file because bash is not a given on the closet PC; the two must stay in step, and the reasoning lives in `dev.sh`.
- [x] **The frontends are gated on `/health`, not started alongside the server.** Launching all three at once is what produced a UI that loads, lets a student scan, and only then says it cannot reach the server. `up` polls `/health` until it answers, and if the server exits first it prints the last 20 lines of its log and stops. It also reuses an already-healthy server rather than starting a second that cannot bind, and refuses to continue when something holds the port but fails `/health` — the exact state a running server plus a stopped Docker leaves behind.
- [x] **The web app's port is pinned** (`vite --port 5173 --strictPort` in `web-app/package.json`, not in the script, so a bare `npm run dev:web` gets it too). `localOrigins` allows 5173 and nothing else, so Vite's default drift to 5174 turns every request into a CORS failure — and a blocked preflight and a dead server throw the *same* `fetch` error, so the UI blames the server. Failing to start says what is actually wrong. The same reasoning is why `up` warns when `.env` sets a `SERVER_ADDR` other than `127.0.0.1:8080`, which `DEFAULT_BASE_URL` hardcodes.
- [x] **The keystroke buffer follows edits; the field is what signs you in.** The scanner buffer only ever grew — Backspace is not a printable character, so it was ignored — and on the sign-in screen that meant typing a number, deleting it, and pressing Enter signed you in as the *deleted* number, with an empty box as the only evidence. Two fixes, because they cover different failures: `scanner.ts` now drops a backspaced character, resets on Delete/Escape, and marks any edited burst as typed (editing is something only a person does, and `fast` is the branch that signs somebody in with no password); and the sign-in screen reads the **field's value**, not the buffer, on the typed path. The field is authoritative because editing is the one thing a keystroke buffer cannot model — caret moves, select-all-and-retype and pasting over a selection all desynchronise it, and only the box reflects what the person can actually see. Six regression tests in `scanner.test.ts`, verified to fail without the fix.
- [x] **Every password field has a reveal toggle** (`components/app/password-input.svelte`). These are typed on a shared closet machine, at a keyboard nobody chose, in a hurry — the worst case for a masked field — and the only feedback a typo gets is a failed sign-in that is deliberately indistinguishable from a wrong password (§7). It is a `<button type="button">`: a bare `<button>` in a form submits it, so a toggle that forgot this would attempt a sign-in every time somebody checked what they had typed.
- [x] **A burst is a scan only when the field agrees with it** (2026-09-15, hardening the entry above). The keystroke buffer following Backspace closed one gesture; it left two. Any *chord* — `Alt/Cmd+Backspace` to delete a word or a line, `Cmd+A` then overtyping, `Cmd+V` over a selection — was dropped by the "a modifier is never part of a barcode" guard before the buffer could react, so the field went empty while the buffer still held `123456` and `fast` stayed **true**: the original wrong-account sign-in, one keystroke further out. And `fast` was only ever reset by Enter or Escape, so a reflex Backspace at an empty field demoted the *next* card scan to typed, demanding a password from a roster-imported user who does not have one yet (§7) — the fix for a wrong sign-in had become a way to block a right one. Three rules now, layered deliberately: a chord or a caret key **discards** the pending burst, because neither says how much text moved; Backspace that empties the buffer **resets** rather than leaving `fast` false, since nothing pending is nothing to doubt; and the sign-in screen treats a burst as a scan only when `target.value` equals the buffer put through the field's own digit filter. The comparison runs through that filter, not raw, or an item barcode scanned at the sign-in screen would never match — its letters are stripped on the way into the box — and the "that looks like an item barcode" message (§15 Q4) would be lost with it. The filter is also why the *typed* path now falls back to the buffer when the two agree: reading the field unconditionally made a typed `12a34` arrive as `1234`, so "Student numbers are digits only" became unreachable and a field emptied by the filter produced no message at all.
- [x] **`up` tears down exactly what it started.** The `EXIT` trap is armed before the first precondition is checked, and cleanup stopped Supabase unconditionally — so a missing-tool error tore down a stack another terminal was using, and the reuse-an-existing-healthy-server path pulled the database out from under the server it had just decided to leave running, manufacturing the "holds the port but does not answer /health" state the same function refuses to handle. `up` now probes Postgres *before* `supabase start` (which stays, being idempotent) purely to record ownership, and cleanup is silent when this run started nothing. Mirrored in `dev.ps1`.
- [x] **The Postgres probe is bash's own `/dev/tcp`, not `nc -z -G`.** `-G` is a BSD-only connect timeout, so on GNU netcat, on busybox, or on a machine with no `nc` at all the probe fails outright and a healthy database reads as down — which `dev.sh test` then reports as a FAILED pgTAP run rather than a passing one, i.e. the one failure mode the "a skip must never pass for a success" rule exists to prevent, inverted.
- [x] **Browser tabs open on readiness, not on launch.** `up` opens the web app and Studio once each URL answers, because a tab opened at a port nothing is listening on yet shows a browser error page as the first thing the user sees, which reads exactly like a broken app. `--no-open` skips it. Every URL goes to `open` in **one call**, which is one window with a tab each; a call per URL is a window per URL. Each service also tees to `.run-logs/` (gitignored), so a crash three minutes in leaves something to read instead of scrollback that has gone by.

**Closed (2026-09-17, Phase 7 built: the panel, and four bugs the browser found)**
- [x] **Backup configuration is a screen, not a file.** Admin → Settings, one card per concern, each saving on its own because `SettingsInput` is a partial update — a page-wide Save would make every half-typed field in an unrelated card a hazard. The two secrets come back **blank with a `_set` boolean**, never masked: a mask round-trips, and the panel would eventually write `••••••••` back as the literal new token. Leaving a secret field empty means "leave it alone"; removing one is its own button.
- [x] **A number field does not hand back what is in the box.** The settings screen used `<input type="number">`, which Svelte binds through `valueAsNumber`: an emptied field arrives as `null`, `12abc` arrives as `null`, and neither is distinguishable from a field nobody touched. Clearing "Keep backups for" and pressing Save showed the admin `Cannot read properties of null (reading 'trim')` — measured in the browser, not reasoned about. Every one is now `type="text"` with `inputmode="numeric"`, which binds the characters actually on screen, and `whole()` takes `unknown` so the guard survives somebody changing the binding back.
- [x] **The restore report is a panel, not a dialog, and sign-in stops fighting for focus.** A restore clears every session as its last act, so the report has to outlive the screen that started it — it lives in `stores/restore.svelte.ts` and renders at the app root, above sign-in. As a modal `<Dialog>` that **hung the tab**: the dialog trapped focus, sign-in's refocus-on-blur (which exists so a keyboard-wedge scanner always has somewhere to type, §10) pulled it straight back out, and the two looped until the page stopped responding and had to be closed. Two fixes, because they cover different failures: the report traps nothing, and `refocus` now stands down when `relatedTarget` names a real element or an open dialog is on the page. The dialog check is `:not([data-state='closed'])` rather than a bare role match — a dialog lingers through its close animation, and a guard that counted those would leave the field unfocusable and the scanner silently dead.
- [x] **A scan while the command palette is open still checks the item in.** `Cmd/Ctrl+K` was the last open Phase 6 item. Its input has focus, so the root `<ScanListener>` is deaf to it by design (§9.3) — which would have made scanning a camera at that screen type a serial into a search box and nothing else, the one thing a barcode must never do (§1.5). The palette carries its own scoped listener: a burst at scanner speed closes it and goes to `POST /scan`; a *typed* burst is left alone, because that is somebody searching for a serial by hand and the list already has the answer.
- [x] **`shadcn-svelte`'s `command-dialog` put its `sr-only` header outside the content.** So the palette's title and description were in the accessibility tree on **every** page, open or not — verified as a live 1×16px node above the browse screen — and the dialog had nothing to point `aria-labelledby` at once it did open. Moved inside `Dialog.Content`. Recorded here because it is a deliberate divergence from a generated file, and a regenerate would undo it.
- [x] **`go test ./...` runs packages in parallel against one live Postgres.** Untidy until the restore tests, whose whole job is to truncate every table and load a backup over the top: `internal/stockroom` and `server` ran concurrently and a neighbouring package's rows vanished mid-test, so the failure landed on whichever test happened to be running rather than on the one that caused it (`TestBackupThenRestoreOverHTTP` = 500, green in isolation). `-p 1` in `dev.sh`, `dev.ps1` and CI.
- [x] **`RestoreResult` gained `by_name`.** `by` is an account id, which is right in a log line and wrong on a screen: the panel was telling the admin who had just pressed Restore that it was performed by `00000000-…-020`. Resolved **before** the tables are replaced, because afterwards that row may belong to somebody else or not exist.
- [x] **The staleness banner renders the server's sentence and only that.** `BackupWarning.admins` stays on the wire as the structured form, but the student-facing message already names them ("Please tell Admin Admin or Test User."), and printing the list again underneath is the same fact twice in two wordings. Hue sits on the icon, never as a fill: §1.2 reserves the five status hues for asset state, and an amber panel reads as an item that is due soon.
- [x] **Below 900px is checked, not assumed.** §7.2 calls it a courtesy rather than a target; measured at 860, 700 and 640px across browse, cart and all six admin tabs — no horizontal overflow anywhere, sidebar collapses to the sheet, hamburger appears.

**Closed (2026-09-18, Phase 8: kits)**
- [x] **A kit is a label over assets, not a thing that can be checked out.** Nothing in `kits.go` writes a custody row: adding a kit to the cart expands it into its asset ids in the frontend, and `CheckOutAssets` commits them exactly as it commits any cart. The alternative — a kit-shaped checkout endpoint — would have been a second definition of the 7-day cap, the overdue block and the custodian rule, and the first one to drift would have been the one nobody tested. It is also why the feature is small: custody stays a fact about a unit, so the history, the overdue rule, the scan branch and the backup all keep working without knowing kits exist.
- [x] **An asset belongs to at most one kit** (`docs/adr/0002`, unique index `kit_items_asset_key`). A shared unit means checking out kit A silently makes kit B incomplete, and that failure surfaces at the shelf, to a student who cannot fix it. Refusing moves it to kit-building time, where an admin can. `AddAssetToKit` inserts and reads the offending row back rather than pre-checking, so there is no check-then-insert window and the refusal names the kit that already holds the unit.
- [x] **A kit name is unique, case-insensitively** (`kits_name_lower_key`). It is what somebody reads off the tape on a bag, and "Kit #1" and "kit #1" name the same bag.
- [x] **Added whole, returned per unit.** The two halves are deliberately asymmetric. **Add to cart** is refused unless every unit is on the shelf, because half a kit is a camera with no lens and the person carrying it finds out at the shoot. `CheckInKit` is the opposite: the units are physically on the counter, and refusing all four because one was already back would leave the database claiming somebody still holds items they returned. So a return is one transaction per unit and the result has three buckets — `returned`, `already_in`, `failed` — never a bare count.
- [x] **The press re-checks what the row claims.** `checkable` comes from the server and was true when the list was fetched; on a shared closet machine somebody else can take a unit in between. `lib/kits.ts` re-decides per unit at press time and reloads the list when it disagrees, so the cart never quietly accepts an item that is already in a bag — the failure would otherwise land as a 409 at checkout naming an item the person never chose.
- [x] **One Kits screen, not a browse one and an admin one.** A student reads it to take a kit out; an admin edits the same rows in place, with the controls simply absent for everyone else, the way the sidebar's Admin group is. Two lists of the same rows would have meant the admin's going stale.
- [x] **Kits sit under the category tree in the sidebar, not inside it.** A kit's units come from four different Types, and the tree's whole meaning is where a *unit* files.
- [x] **Deleting a kit is allowed while its units are out**, unlike deleting an asset. The asymmetry is the point: an asset delete cascades `custody_events` and would take the trail with it, while a kit owns no custody — `kit_items` cascades and the grouping is all that disappears.

**Closed (2026-09-18, backup system backtested end to end)**
The whole of Phase 7 re-verified against a live database — a real archive on disk, a truncate-and-restore round trip, the disaster path with zero accounts, and every refusal — which found three defects, all in the *reporting* rather than in the data path:
- [x] **A target that has never succeeded is not a target that is late**, and the sentence has to say which. The local archive always succeeds, so from the first misconfigured off-site target the staleness came from GitHub while the *age* came from local — and all three surfaces, including **every student's sign-in**, read "Backups have not run in 0 hours." The rule already existed in a comment ("has not run in 0 hours is the kind of message that makes a person stop reading warnings") but the guard only covered the case where nothing at all had succeeded, which is the case a running site never reaches. `staleSentence` now takes the age, whether anything is past the threshold, and which targets have never managed a run.
- [x] **"Test connection" now answers with the reason.** It existed so a misconfiguration is found by somebody standing at the machine, and a wrong token answered `500 {"error":"internal error"}` while the log held `github 401 Unauthorized: Bad credentials` — `writeError`'s default branch logs and hides anything without a sentinel. A target that says "bad credentials" is a *setting* that is wrong, so it is `ErrInvalid` and the message survives; a sentinel the target already chose (`ErrNotConfigured` → 503) is passed through untouched.
- [x] **A forced restore says it was forced.** `force` is an admin overriding a refusal — a schema-version mismatch, or a live table the archive does not carry — and the report came back with an empty `warnings` list, identical to a restore that needed no override. Both overrides now name themselves on the report, and the second one says outright which table was emptied.

What the backtest confirmed unchanged: every checksum in the manifest recomputes, every CSV's row count matches it, `sequences.csv` carries `last_value` **and** `is_called`, the redacted `github_token` never reaches `app_settings.csv` and is put back rather than nulled by the restore, a tampered CSV is refused *before* the transaction opens with the database untouched, a wrong passphrase is refused, encryption replaces the plaintext zip and both targets exclude the readable CSVs when it is on, two concurrent runs make one a skip rather than a failure, dated folders prune at `keep_days`, the photo mirror rolls over and its live generation cannot be deleted, and `cmd/restore` brought a database with **zero accounts** back from an encrypted archive with a typed passphrase — after which all three accounts signed in again. The boot catch-up was observed firing for real: it logged "no backup has ever completed on this machine; running now" ten seconds after start-up, once a folder had been configured.

**Closed (2026-09-18, the photo wall's §7 and §8: admin control and configuration)**
- [x] **A Drive folder link is a credential, so the field is write-only and there is no way to read one back.** A folder shared "anyone with the link" is readable by whoever holds the URL, so echoing the live link into the admin panel would put the whole folder one copy-paste away from anyone standing at a machine left open on that screen. The rule already existed for `app_settings.github_token` and for every password field; the folder id joins it in four places at once, because any one of them left out defeats the other three: the panel never renders it, no HTTP response contains it (asserted by a test that greps both response bodies for the id *and* for `drive.google.com`, so a debug field added in a year fails the suite), `activity_log` records the folder's label instead, and `exportRedactions` keeps it out of `app_settings.csv` — missed, the first nightly backup would push the key to the department's photographs into the backup repository, the precise failure §11 already anticipated for the GitHub token.
- [x] **The folder is named by a label an admin types, not by a name fetched from Drive.** §7 asked the screen to show "the folder's name, not its ID", which reads as a Drive lookup and is not one that can be written: rclone addresses Drive by path from a configured root and has no "stat this file id" command, so with only an id in hand there is no way to ask Google what the folder is called without the Google API client §3 exists to avoid. A typed label is the honest version of the same affordance — readable, and it opens nothing. It is required rather than optional, because an unnamed folder makes the status block and the audit row say nothing at all.
- [x] **The probe is the feature.** `PUT /admin/photo-wall` parses the link, runs one **bounded** listing against that id with the configured credential, and only then writes. Bounded (`--max-depth 1`) because an admin is standing at the screen and §3's recursive listing of a large folder takes minutes. An *empty* folder passes: §9 says a folder with no usable photographs is permitted because it may be mid-upload, and refusing it would break the panel in exactly the moment somebody is setting the feature up. Without the probe a typo returns a success message and the wall silently empties ten minutes later, with nothing on any screen connecting the two events.
- [x] **A switch is a write plus a teardown, in that order, and the generation counter is what makes it safe.** Write the row and the audit line in one transaction, then `SetFolder` (drop the manifest, nudge a rebuild) and `Invalidate` (advance the generation, delete every *ready* tile) together. Tiles already **served** are left to expire on their TTL — their URLs sit in a browser that has already rendered them, and 404ing a live page to save fifteen minutes is the worse trade. The generation was built in §5's step for this moment: a fetch in flight carries the generation it began under, so a slow `rclone cat` cannot deposit a photograph from the replaced folder into the new reel minutes later.
- [x] **The preview does not consume.** `PreviewPhotos` returns ready tiles without flipping them to served, so an admin reloading the screen cannot empty the buffer the sign-in screen is about to draw from — which would make the screen's own confirmation the thing that breaks the wall. Verified live: three previews in a row left the reel at 48 ready / 0 served. The cost is that a preview URL can go stale, and the strip hides a 404 the same way the wall does.
- [x] **The live folder moved into `app_settings`; `.env` seeds it once.** `SIGNIN_PHOTOS_FOLDER_ID` is now a first-boot seed beside `BACKUP_DIR`, `PHOTO_BACKUP_DIR` and `RCLONE_REMOTE`, and `main.go` reads the folder out of the row rather than out of the config. It has its **own** marker, `signin_photos_env_seeded` (`20260921090000`), rather than the `env_seeded` one the three backup values share — a divergence from `docs/design/signin-photo-wall.html` §8, and the one thing here that is not what the design said. The markers differ by a day: `env_seeded` shipped with the columns it guards, so its migration could skip the backfill on the grounds that no installation was seeded-but-unmarked, while the folder column arrived afterwards, by which time every database that had started the server once already had `env_seeded = true`. Sharing it gave the folder a `where not env_seeded` that is false on precisely the installations it was written for, so `.env` would pre-fill a database created after the feature and be silently ignored on every one created before it. One marker per seeding pass, each defaulting to false, so a value added later is a first-boot seed on an upgraded machine too. Backwards would mean a folder an admin pasted reverting on the next restart. `SIGNIN_PHOTOS_REMOTE` stays in `.env` and stays the switch: it is an install-time value, and unset means no reel, no goroutine and today's sign-in screen.
- [x] **The sidebar item is "Photo wall" and it is last, not "Google Drive" directly under Backup** — a divergence from §7, recorded because it is deliberate. A tab called Google Drive would sit two rows under a Backup screen that also pushes to Google Drive and name the transport rather than the thing; and Backup and Settings are one subject read top to bottom, which an unrelated item wedged between them would break. It is also the only admin screen that is decoration rather than inventory.

**Closed (2026-09-21, first real off-site run)**
The Drive target was pointed at a real Google account for the first time and the local target at a folder chosen in the panel rather than by a test. Both worked on the first attempt; what the session found was a third thing, in the settings screen rather than in either target.
- [x] **A backup folder must be a full path, and a relative one is refused rather than resolved.** The folder was typed — realistically, pasted out of a Finder window — as `Users/<you>/Desktop/Testing for stockroom`, without its leading separator. Every layer then did its job perfectly: the run wrote a complete archive, the manifest verified, `.last-success.json` was written, the status screen and every sign-in reported a healthy backup. The files were in `<repo>/Users/<you>/Desktop/…`, resolved against the server's working directory, and the Desktop folder the admin kept opening held nothing. That is the failure this subsystem exists to make impossible (§11: "a backup system whose failure mode is silently not backing up"), reached through the one door nobody had locked. Resolving the path against the working directory was the alternative and is worse: it means the same setting names a different folder under `dev.sh`, under a Windows service starting in `System32`, and under launchd — so the value would be correct on the machine it was typed on and wrong on the one it matters on. `validateDir` is called **per field from `SaveSettings`, not from `validate()`**, because every card on the settings screen saves on its own (2026-09-17) and a bad value left in the Folders card must not refuse a save in the GitHub card while naming a field the admin cannot see from there. `.env` keeps its relative form — `BACKUP_DIR=./backups` beside a developer's repo is reasonable — and `EnsureSettings` makes it absolute at seed time, so whatever writes the column, the column means one thing.
- [x] **A stored relative folder is refused at the run, not only at the field.** `validateDir` guards what an admin types; nothing rewrites a column that was already there, and the database that produced the decision above held exactly such a value. So `backupDir` refuses a folder that is not a full path with `ErrNotConfigured`, and `photoBackupDir` answers `""` — the unconfigured state every caller already handles, which the backup screen and every sign-in already say out loud — with `PhotoMirrorStatus` naming the folder so the two kinds of empty are not one message. Resolving it at use time is the alternative and is the same silent success: a complete archive, a manifest that verifies, every screen reporting health, and the files under whatever directory the server started in.
- [x] **The Drive push excludes what a file manager leaves behind.** `Push` copies the whole backup directory, deliberately, so a night the uplink was down is caught up later. An admin opening that folder in Finder to check on it puts a `.DS_Store` beside the archive, and the next push carried it to Drive — observed, 6 KB of it. The off-site copy must hold what the run wrote and nothing else, or nobody auditing what left the machine can say where a file came from. `.DS_Store`, `._*`, `Thumbs.db` and `desktop.ini` join the log and state-file excludes. The GitHub target enumerates its files explicitly and never had the problem.
- [x] **A test that only passed because nobody had configured Drive.** `TestSettingsAndStatusRoutes` asserted that testing an unconfigured target answers 503 — true until somebody connected a real Google account to the shared dev database, after which it was a red build saying nothing about the code. It now clears the Drive settings for that one assertion and restores them in a `defer`. The rule it stands for: a test that reads shared configuration has to establish the state it is asserting about, not hope for it.


**Closed (2026-09-22, the photo wall's §9 and §10: the first real Drive folder)**
The sign-in photo wall was pointed at a real folder for the first time — 2,274 files, 2,073 of them usable — on a read-only rclone remote configured by hand (`docs/design/signin-photo-wall.html` §3, step 2 of §11). The suite had been green throughout; every one of the four defects below lived in a join a stub stood in for. §9 of the design doc records each fix and the judgement calls behind it, and §10 lists the tests. Each fix was checked by breaking it and watching its test fail.
- [x] **The listing decodes rclone's real output.** Under `--no-modtime` rclone still prints `"ModTime":""`, and the wall was decoding into the backup's `rcloneEntry`, whose `time.Time` refuses an empty string — so every real listing failed on its first line. Every fixture had left the key out, the one shape rclone never produces. The wall now has its own three-field `photoListingEntry`; the backup, which lists without that flag and does read the time, is untouched.
- [x] **Waiting for the manifest is not a failure.** "Still listing" and "no folder chosen" were counted toward the filler's 25-failure rest, so a one-minute first listing earned a five-minute rest before it had finished and the wall came up about twelve minutes after boot. The source tags those two normal states (`errPhotoWallWarmingUp`), and the reel neither counts them nor sets an error, asking again at the ten-second idle tick. A failed listing or an empty folder still counts. Measured after the fix: the first tile 16 s after the listing.
- [x] **The admin screen explains a resting reel, and the log says it once.** The status read only the source's listing errors, so two thousand photographs, zero tiles and a resting filler showed no error. It now reads the reel too, but only while it rests — a single failed download among successes is normal and was seen on the first run. A streak of ratio-gate rejections gets §9's sentence naming 3:2. §9's "logged at debug" became one line when a streak reaches the cap and one when it recovers, because the server has no debug level; nothing per failure.
- [x] **rclone's errors carried the folder id to the admin screen.** A failed Drive call quotes its request, whose query string is `'<id>' in parents`, so an expired token or a dropped network would have put the id into `last_error` — the one thing §7 promises no response carries, and a test guarding it had only ever seen the happy path. Every rclone error the wall keeps now goes through `explain`, which removes the id in all three encodings, reduces the request to the word Drive, and drops rclone's timestamps and client-id notice. It also recognises an expired sign-in and names `rclone config reconnect <remote>:` with the remote as configured, since rclone's own suggestion names an internal `{hash}` form nobody can type; logged once per outage, and a 503 rather than a sharing complaint behind the Photo wall save. The cleaning is the wall's alone: the backup's `runRclone` carries no secret id and was left as it is.

**Closed (2026-09-22, the production install path)**
The one thing standing between this and a machine that runs unattended was that installing it meant
being a developer, and that running it meant somebody having left a terminal window open. Five
decisions, all verified against a scratch database with no Supabase anywhere.
- [x] **Supabase is a development tool, not a deployment.** An install runs one `postgres:17`
      (`deploy/docker-compose.yml`) and development is completely unchanged — `supabase start`, `db
      reset`, `seed.sql`, Studio and pgTAP all still work, verified by a full reset and a green
      suite after the change. The container count was the obvious argument and not the decisive
      one: shipping the stack puts **Studio on the closet PC, on port 54323, with no
      authentication** — read and write on every table, the roster included, for anyone who walks
      past and opens a browser. The port binding is written `127.0.0.1:54322:5432` rather than the
      short form for the same class of reason: Docker's default publishes on every interface *and
      punches its own hole through the host firewall*, so the short form would have put the database
      on the school network. `POSTGRES_PASSWORD` has no default, so a compose file run without one
      refuses to start rather than booting a closet machine with `postgres:postgres` and never
      mentioning it.
- [x] **The migrations are embedded, and they are the same files, in the same table.** The embed
      lives in `supabase/embed.go` — the directory the CLI already reads — rather than a copy under
      `internal/`, because two copies are two schemas that agree until somebody edits one, and the
      failure then is a production database shaped differently from every test that passed against
      it. The bookkeeping table is **Supabase's own** `supabase_migrations.schema_migrations`, not a
      second one: with two tables, a database migrated by the CLI looks unmigrated to the server, so
      the first production start after a `db reset` would replay `create table profiles` over a live
      schema. It is also the table `schemaVersion` already reads for the backup manifest, so the
      restore's version check keeps working on an install that has never had the CLI near it.
      `Migrate` is **fatal** where the failsafe admin and the backup settings are not — those are
      features that can be absent, while a wrong schema is a server that fails on its first real
      query, at a counter, with a student holding a camera. A file whose name carries no version
      prefix is an error rather than a skip, because a migration silently not running is the exact
      failure the mechanism exists to prevent.
- [x] **`20260826180000_grant_service_role.sql` had to be made portable, by editing an applied
      migration.** `service_role`, `anon` and `authenticated` are created by the Supabase stack and
      do not exist on a plain Postgres, so unguarded this file stopped a fresh install at migration
      two of ten — with an error naming a Supabase concept the person reading it had deliberately
      never installed. It is now guarded on the roles existing, via `execute`: a literal `GRANT`
      inside an `if exists` still fails at *parse* time, because the role is an identifier compiled
      with the block. Editing an applied migration is safe here precisely because both readers track
      by version and not by content.
- [x] **One binary, serving the UI from the same origin as the API.** `web-app/embed.go` mirrors
      what `desktop-app/main.go` has always done. Vite's `assetsDir` had to move from `assets` to
      `static`, because the API already owns `GET /assets` and `GET /assets/{id}` — a default build
      puts `/assets/index-a1b2c3.js` inside the equipment catalogue's route and the router answers a
      JavaScript request with "asset not found". Renaming the bundle directory is the fix; renaming
      a documented endpoint (§8.1) to suit a bundler is not. Same-origin serving also takes CORS out
      of an install entirely — a same-origin request is not subject to it — which removes the single
      most confusing failure this system can produce (§13, 2026-09-15: a blocked preflight and a
      dead server raise the same `fetch` error, so the UI blames the server). The allow-list stays,
      because the dev loop and the Wails window really are separate origins. `dist/.gitkeep` is
      tracked so `go build ./...` works on a fresh clone, and `webapp.Present()` distinguishes
      "nobody ran `npm run build`" from "the UI is missing" — identical bytes, very different
      problems — so the server says which, in words, instead of serving a blank page.
- [x] **The service is not optional, and on macOS it is an Agent rather than a Daemon.** The nightly
      backup is a goroutine inside the server (§11), so "the server is running" and "backups happen"
      are one fact, and until now it depended on somebody having left a terminal open. Both
      platforms start `deploy/stockroom-run.sh` — one entry point, so there is no macOS copy and a
      Linux copy to drift — which waits for the **Docker daemon** rather than the binary (the
      difference is the whole failure mode on a machine that has just booted) and then `exec`s the
      server, so the service manager supervises the real process rather than a live wrapper around a
      dead one. The Mac gets a LaunchAgent because **Docker Desktop only runs inside a user
      session**: a boot-time daemon would start, find no Docker, and give up. The accepted cost is
      that the closet Mac has to be set to log in automatically, or a power cut leaves it at the
      login window — running nothing, backing up nothing, saying so nowhere. `docs/INSTALL.md` says
      that in those words. Linux has no such constraint and gets a real system unit.
- [x] **Durability was measured, not assumed.** The question "if everything shuts down for whatever
      reason, is the data still there" has a real answer and it was worth getting empirically. The
      database container was SIGKILLed outright, with no clean shutdown of any kind, after an asset
      had been created through the API; on restart Postgres ran WAL crash recovery ("redo starts at
      0/14EF878 ... redo done") and the row was intact. The Go server's pgx pool reconnected on its
      own once the database answered again, with no restart of the server. And launchd was watched
      bringing the server back **two seconds** after a `kill -9`. What this leaves: `docker kill` is
      deliberately treated by Docker as a *manual* stop, so `restart: unless-stopped` does not fire
      for it — it fires for a genuine crash and for the machine coming back, which are the cases
      that matter. `stop_grace_period` is set to a minute because Docker's default is **ten
      seconds** before it SIGKILLs, and a fast shutdown whose checkpoint has a lot of dirty buffers
      can want longer; being cut short is not data loss, it is crash recovery on every restart and a
      frightening log on a morning when nothing is wrong.
- [x] **Re-running the installer is the upgrade, and it dumps the database first.** From inside the
      container, so no Postgres client is needed on a host that by design has none. A dump that
      fails **stops** the upgrade rather than continuing: it is the only protection against a
      migration that goes wrong, and an upgrade that skips it silently is not a trade anybody would
      agree to if asked. `.env` is never overwritten, because the backup folder, the Drive remote
      and the photo-wall folder are first-boot seeds that `app_settings` owns from then on — an
      installer that rewrote them every release would be the "a value an admin typed reverts on the
      next restart" failure the Phase 7 decision rules out.

**Closed (2026-09-22, Phase B: the first-run experience)**
Phase A made it installable; Phase B makes a fresh install usable by a teacher who has never seen it — no accounts, no categories, no items, no labels.
- [x] **The wizard gate diverges from the plan.** The plan said the wizard exists only while `profiles` is empty. On the installs this project actually produces, that table is **never** empty when a browser first opens it: `install.sh` asks for the failsafe admin and the server creates that account at boot. Gated on an empty table, the wizard would never appear. So only **creating the first admin** keeps that gate — it is the one unauthenticated write in the system, and the check and the insert happen in one transaction under a table lock, so two browsers racing the form make one admin and one refusal. Everything after it is an ordinary admin-only call, gated on `app_settings.setup_completed_at`, which the migration sets on any database that already has accounts so an upgrade never lands a working school in the wizard.
- [x] **The failsafe is written into `.env`, not the database.** Its whole job is getting back in after the database is lost, so the database is the one place it cannot live. `setEnvValues` is the only code that writes `.env`: temp file plus rename, the file's mode kept, values single-quoted, every other line byte-for-byte untouched. The wizard refuses the admin's own number (the failsafe's password is re-applied at every start, so it would silently overwrite theirs) and any ordinary account's (which it would promote to admin). If the installer already made one, the step says so rather than making a second.
- [x] **Example rows are recognised by content, not by a reserved UUID.** They go in through the real importers, which generate their own ids, so a reserved id would have meant a second, example-only code path. They are recognised by two things a real row will not have together: an `EXAMPLE-` serial **and** a name starting `Example`, or a `90000x` number **and** the first name `Example`. Loading skips a half whose keys a real row already holds rather than merging into it — found by a test: the roster upsert would have renamed a real student numbered 900004 to "Example Student-Four", and "remove the examples" would then have deleted them.
- [x] **The example serials are short** (`EXAMPLE-CAM1`, not `EXAMPLE-CAM-001`). `<Serial>` middle-truncates past 14 characters, so five examples all rendered as the same `EXAM…-001` — a demo of the catalogue that made every item look identical.
- [x] **Serials have a practical length limit, and it is the small label.** Code 128 widens with every character, and a bar thinner than 0.25 mm did not decode at 203 DPI (measured with `zbarimg`, which also moved the floor up from the quoted 0.19 mm). The small label fits about 9 characters on Letter and 7 on A4; §6.2's examples were shortened to match.
- [x] **Bugs the Phase B tests found, all fixed:** the asset import could never create an item (it matched `ErrNotFound` on an error `mapPgError` passes through as `pgx.ErrNoRows`, so every new serial failed); every bulk add was a 500 (`RETURNING` used the `a.` column list without aliasing the table); the category import ran in one transaction without savepoints, so the first name collision aborted it and every later row was reported as failed too; the text parser counted a tab as half a level, putting a tab-indented child beside its own parent, and turned every line of Markdown prose into a top-level Type; the label and bulk handlers dropped `decodeJSON`'s error and answered malformed JSON with an empty 200; and loading the examples could rename, then delete, a real student numbered 900004 (above).
- [x] **Replacing the failsafe retires the old one.** `ConfigureFailsafe` rewrote `.env` and ensured the new account, but the previous failsafe stayed an admin with its old password for good — nothing re-applies it any more, and "the old one may have leaked" is the likeliest reason to replace it. Every other failsafe-created account is now demoted, its password cleared (not deleted, so custody history naming it survives) and its sessions dropped — every other one, not the number `.env` held a moment ago, so a retry after a partial failure still retires it.
- [x] **A digits student number is ASCII digits.** `unicode.IsDigit` also accepted Arabic-Indic and fullwidth digits, which Code 128 cannot put on an ID card and which made `１２３` a different account from `123`.
- [x] **The asset import reports real file lines and survives a malformed one.** The line was a record counter, so a blank line or a multi-line quoted field made every later row name the wrong spreadsheet line; it is now the reader's `FieldPos`. A bare quote mid-file returned a 400 that hid the rows already committed — it is now a failed row in the report, and the run stops there.
- [x] **An upgrade polls the address the server actually listens on.** `install.sh` keeps the existing `.env`, so it now reads `SERVER_ADDR` back from it rather than health-checking the default for three minutes and reporting a successful upgrade as a failure.
- [x] **The settings are loaded before the failsafe is ensured.** The old boot order validated an `AB12345` failsafe against the digits default, logged a warning and started with no way in — on exactly the school the format setting exists for.

**Closed (2026-09-23, Phase C: publish and prove)**
The half of Phase C that is code and documentation. The other half (a clean machine, a stranger, a pilot, a tag) needs people and hardware and is still open there.
- [x] **Export everything is the backup's archive, not a second exporter.** `takeSnapshot` in `export.go` is what `runBackupLocked` used to do inline, from the repeatable-read transaction to the finished zip, and both the nightly run and `ExportEverything` call it. A second exporter would be a second definition of "every table", and the first migration that added one would find out which of them had been forgotten. What differs is everything around the archive: no backup folder (a school that never set backups up can still leave), no advisory lock (it writes nothing to disk that another run could collide with), no targets, and **never encrypted**, because the person leaving Stockroom has to be able to open the file and the passphrase exists for archives that leave the machine unattended. The redactions still apply. Named `stockroom-export-*` rather than `backup-*` so a teacher looking for "export" finds it. Because it carries the same manifest, it restores, which makes "move to another PC" an export plus an upload; the HTTP test does exactly that round trip.
- [x] **The GitHub target says it is untested, in the three places a school chooses it.** A tag and a sentence on its Settings card, a word beside it in the Backup screen's target table, and a callout in `docs/BACKUP-SETUP.md` Step 4. The advice is to use it as a second copy and restore from it once before relying on it, which is the test the code has not had.
- [x] **Windows CI boots the production binary, and that is the migration test there.** The runner has no Supabase CLI, so `tests-windows` starts the image's own PostgreSQL, builds `stockroom.exe`, starts it against the empty database and waits for `/health`, then loads `seed.sql` with `psql` and runs the Go suite, the type check, Vitest and the build. That is closer to a school's machine than the Linux job, where the CLI applies the schema. Writing it turned up the first Windows-only defect before it ever ran: `settings_test.go` used `/from/dot/env` as an absolute folder, which is relative on Windows and which `validateDir` rightly refuses. Its first run found one more: `TestSetEnvValuesRoundTrips` asserted a `0600` file mode, which Windows cannot report (Go shows every writable file as `0666` there), so that assertion is Unix-only now. Every other test passed on Windows on that first run, the restore round trips included, against a schema the binary applied itself. It also parses `dev.ps1`. pgTAP stays Linux-only.
- [x] **The push loop is `pushEach`, split out of `pushToTargets`** so a test can hand it a target that fails. The carried-over "run both targets, break one" check is now `TestOneBrokenTargetLeavesTheOtherWorking`: the broken target goes first, the good one still pushes, and the status and its warnings name the one that failed and not the other.
- [x] **The history has no secrets.** gitleaks over all 348 commits found 109 hits, every one the Supabase CLI's `supabase-demo` service_role JWT from the deleted `desktop-app/frontend/src/lib/supabase.ts` (§4): public, identical on every local stack, signing nothing. Checked by decoding the payload, then recorded in `.gitleaksignore` with that reason, so the next scan is clean and anything it does find is new. The working tree holds nothing tracked that should not be: `uploads/` is empty and ignored, and so are `.env`, `.env.bak.*`, `.run-logs/`, `.backup-test/` and `.build/`. The only real personal string in the docs was the maintainer's macOS username in the 2026-09-21 entry above, now `<you>`; every other name and number is seed, fixture or example data.

**Closed (2026-09-24, the photo wall's Google sign-in)**
Until now the photo wall could only be turned on by editing `.env` and restarting the server, and its rclone remote had to be made by hand with `rclone config` — the Photo wall screen's first instruction was the one the Phase 7 rule (no admin ever edits a file) exists to rule out.
- [x] **Signing in to Google is the switch, reversing the 2026-09-18 "`SIGNIN_PHOTOS_REMOTE` stays in `.env` and stays the switch".** Admin → Photo wall has a **Sign in with Google** button, tagged *Required* until it has been used, and the folder form waits on it. Finishing the sign-in writes the rclone remote and starts the wall **on the running server** — no restart. The wall runs exactly when rclone has a remote for it, so "is it on?" is answered by the credential itself. A flag in `app_settings` was the other option, and it could disagree with the credential: restore the database onto a new machine and it would say on, then fail against a remote that doesn't exist. Asking rclone means the new machine shows the button instead. `SIGNIN_PHOTOS_REMOTE` survives only as the remote's name, defaulting to `gdrive-photos`, for a remote made by hand under another name.
- [x] **The wall's token is `drive.readonly`, and the scope reaches `rclone authorize`, not only `rclone.conf`.** The token's scope is fixed when Google issues it; a `scope = drive.readonly` line written afterwards would sit over a write-capable token and read as safe. rclone takes the option as an unpadded URL-base64 JSON blob (a padded one is refused). Verified against v1.75: with the blob Google's consent URL asks for `.../auth/drive.readonly`, without it `.../auth/drive`. The backup keeps its full-scope remote; the two remotes are separate on purpose. A pending sign-in remembers its scope, so one started on the backup screen cannot be finished into the photo wall's remote (409). A pasted block's scope cannot be checked, so the dialog gives the exact read-only command, which the server supplies.
- [x] **The token is stored in exactly one place, rclone's config file.** Not `app_settings`, so it never enters a backup or the GitHub target. Not any response: the screen learns `google_connected` from `rclone listremotes`, which prints names only. Not the log: an rclone error echoing its arguments has the token replaced before it is shown. `activity_log` records who signed in and the remote's name.
- [x] **The reel and its source are one atomically swapped pair on `DB`** (`photoWall atomic.Pointer`), replacing the two exported fields `main` assigned once at boot. The wall can now start while the sign-in screen and the admin screen are reading it, and two separate fields would let a reader see the new reel beside the old nil source. The tile route looks the reel up per request for the same reason, since it used to capture a nil one when the router was built. `go test -race` is clean over the whole suite.
- [x] **Starting a Google sign-in stops any other one still waiting.** Every `rclone authorize` listens on 127.0.0.1:53682. A second one — Connect pressed twice, or on the backup screen and then here — failed to bind, never printed a link, and surfaced as a 30-second timeout blaming `rclone version`. The new one now stops the waiting one and waits for its process to exit before binding. The backup's Connect benefits too.
- [x] **Reconnect is the same button** (`rclone config create` on an existing name replaces it, verified). It is now the fix for an expired sign-in that used to need `rclone config reconnect` typed into a terminal. A reconnect while the source is failing triggers an immediate re-list rather than waiting out the five-minute retry.
- [x] **The sign-in dialog is one component** (`google-connect-dialog.svelte`), used by Settings and Photo wall, extracted from the backup screen rather than copied from it.

**Still open**
- [ ] **The installer builds from a checkout**, so it needs Go and Node on the machine it is run
      from. `.github/workflows/release.yml` exists and produces the four platform binaries, but no
      tag has been pushed and nothing has been downloaded onto a machine that is not this one, so
      the release-fetch path is deliberately unwritten rather than written untested
- [ ] **Windows has no installer and no service.** `install.ps1` is not started, and `dev.ps1` has
      still never run on real Windows hardware. CI now builds, boots and tests the server on a
      Windows runner and parses `dev.ps1` (2026-09-23), which is not the same as running it
- [ ] **The machine has not been rebooted to prove the service comes back.** A `kill -9` of the
      server was verified (launchd restarted it in two seconds) and so was the database surviving a
      SIGKILL, but a real reboot exercises automatic login, Docker Desktop starting, and
      `restart: unless-stopped` all at once, and none of those has been watched
- [ ] Barcode scanner model (Week 7). Must be plain HID keyboard-wedge
- [ ] Scan-vs-typed keystroke threshold. Ships as a named/configurable constant defaulted to 50ms; tune with real hardware in Week 7
- [ ] Exact backup folder, photo-mirror disk and target credentials on the closet PC (blocked on the PC being provisioned). All of it is admin-panel configuration, so none of it blocked the code — `docs/BACKUP-SETUP.md` is the walkthrough for the day the machine exists
- [ ] **GitHub** (labelled experimental in the UI and docs since 2026-09-23) has not been exercised against a real account: no repository has been created, so the REST API path is unit-tested and hand-read but not yet round-tripped. **Drive is no longer open** — a real Google account was connected on 2026-09-21 and a full run pushed to it (see the entry above). The local target is fully verified end to end
- [ ] **rclone's shared Google client id is being retired during 2026, and nothing in Stockroom can supply a replacement.** Both Drive paths ride on the id baked into the rclone binary: `startDriveAuthorize` runs `rclone authorize drive` and `FinishDriveConnect` / `FinishPhotoWallGoogle` run `rclone config create ... token=...`, none passing a `client_id`, and the string does not appear anywhere in the codebase. So an admin who makes their own has no way to use it for the backup remote, and the panel's Connect button would put it back on the shared id. The work is one Google Cloud project (Drive API enabled, Desktop OAuth client) plus two optional fields threaded into those two call sites — `rclone authorize` already takes `<client_id> <client_secret>` as arguments. The trap to carry with it: an External consent screen left in **Testing** expires refresh tokens after seven days, which on an unattended closet PC is a backup that dies a week after setup and announces it only as a staleness warning. It has to be **Published**; unverified is fine, since `drive` and `drive.readonly` are restricted scopes that cap an unverified app at 100 users and one machine is one user. Internal is not an option because Step 3 of `docs/BACKUP-SETUP.md` requires a personal account rather than the school Workspace one. Deferred knowingly on 2026-09-21: the shared id still works, and swapping later costs one `rclone config reconnect` per remote.
- [ ] Whether the photo mirror gets a second physical disk. Photos are local-only by decision, so this is their only redundancy (`docs/design/backup.md` §H)

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
