# Stockroom: media department asset checkout system

This file is the master reference for the project: what it is, how it's built, how to set it up, and the build timeline. Keep it updated as decisions get made. It's meant to be the single source of truth for anyone (human or AI) picking up this codebase. `TODO.md` tracks the phase-by-phase backend work; this file explains the *why* and the *shape*.

Last major revision: 2026-09-04 (product flow, auth model, and backend architecture pinned down; Section 13). Amended 2026-09-13: browse-list ordering, custodian-field visibility, and the backend deepening pass (one `DB` handle, one category tree, endpoint table in Section 8; Section 13).

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
- Automated nightly off-site backup (CSV pushed to Google Drive via `rclone`)

**Non-goals** (explicitly out of scope; tables may exist in the schema but nothing is built on them)
- Reservations / future bookings / double-booking prevention (maybe much later)
- Physical locations (building/room/shelf). Location is "who has it"
- Free-form tags, saved filter presets, per-asset custom fields
- Multi-station / LAN access. The web app is localhost-only on the closet PC
- Native mobile apps
- Email/SMS reminders (needs internet)
- GPS tagging

**Lowest priority, kept in scope if time allows.** Kits, a named bundle of assets (e.g. "Kit #1 = this camera + this lens + this bag") checked out/in as one unit.

---

## 3. Deployment model

One dedicated Windows PC lives in the camera closet, always on. Development happens on macOS. Three things run on the machine:

1. **Supabase local stack** (Docker). Used purely as the Postgres host (+ Studio for poking at data). Its REST API and Auth services are unused.
2. **Go HTTP server** (`server/`). The only process that talks to the database. Listens on localhost. All business logic, account handling, and role checks live here (in `internal/stockroom`).
3. **Frontends**. Two thin UIs that only speak HTTP to the Go server:
   - **Wails desktop app** (`desktop-app/`). Primary interface, native window, Svelte 5 + TypeScript. Wails' Go side is just a window host; it does not touch the DB.
   - **Web app** (`web-app/`). Vite + Svelte 5 + TypeScript, served on `localhost`, mirrors the desktop app. Localhost only, not exposed on the LAN.

The USB barcode scanner plugs into this machine. Nightly, a Go CLI exports every table to CSV into a local folder, then shells out to `rclone copy` to push that folder to Google Drive. `rclone` owns the OAuth token and refresh handling (set up once, interactively, via `rclone config`); the Go code never talks to the Drive API directly.

---

## 4. Backend architecture (decided): single Go backend over Supabase-hosted Postgres

**The Go server is the only database client.** `internal/stockroom` holds every query, transaction, permission check, and account operation. `server/` wraps it in HTTP handlers. Both frontends will call those endpoints with `fetch` through a small `lib/api.ts` wrapper; no Supabase JS client, no PostgREST, no database credentials in TypeScript.

**Current state (until TODO Phase 6).** The backend is built to that shape, but the frontends are not yet wired to it. `desktop-app/frontend/src/lib/db.ts` and `supabase.ts` still call PostgREST directly with the `service_role` key, and the web app has no data layer at all. Phase 6 replaces both with `lib/api.ts` and deletes the supabase-js path.

Why this shape:
- One place for all logic, written in Go (preferred over TS for this codebase).
- Both UIs share one code path and one session store, so behaviour can't drift.
- Postgres still runs inside the Supabase CLI stack because migrations, seed loading, and Studio are already set up and working. Go connects to the **direct Postgres port (54322)** via `DATABASE_URL`.

**Historical note.** An earlier iteration had the Svelte frontend calling PostgREST directly with the `service_role` key (`supabase/migrations/20260826180000_grant_service_role.sql`, `desktop-app/frontend/src/lib/supabase.ts` and `db.ts`). The migration stays; the TS files go in TODO Phase 6. RLS is off and stays off: only the Go server, connecting as `postgres`, reaches the DB.

---

## 5. Tech stack summary

| Layer | Technology |
|---|---|
| Database | PostgreSQL, hosted by the Supabase local stack (Docker); migrations + seed via Supabase CLI |
| DB access | Go, `pgx/v5` + `pgxpool`, direct connection on port 54322 |
| Backend / API | Go `net/http` server (`server/`) on localhost, JSON endpoints; logic in `internal/stockroom` |
| Auth | Scan login (student number, no password) or typed login (student number + bcrypt password); in-memory session map in the Go server; `is_admin` flag gates the admin panel |
| Files | Profile + asset photos copied into a local `uploads/` dir, served by the Go server at `/files/…` |
| Desktop app | Wails (Go window host + Svelte 5 + TypeScript frontend, Tailwind CSS v4). Calls the Go server over HTTP |
| Web app | Vite + Svelte 5 + TypeScript, Tailwind CSS v4. Calls the Go server over HTTP; localhost only |
| Barcode scanner | Standard USB HID keyboard-wedge scanner. Not yet tested with real hardware |
| Backup | Go CLI (`cmd/backup`) → CSV per table → local folder → `rclone copy` to Google Drive; scheduled by Task Scheduler (Windows) / launchd or cron (macOS) |
| Config | `.env` at repo root (Section 9), loaded into `stockroom.Config`; `Open` takes the parts the package needs as `Options` |

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
- `create unique index on assets(serial_number)`. The serial is the scan key. Barcode stickers encode it. Linear items (batteries, bags, SD cards) use model-prefixed serials like `T7iBat-001`, `T5iBat-001`, `SD-014`.
- `photo_path text`
- `asset_status` enum gains `'unavailable'`. The catch-all for broken/missing/retired. v1 uses only `available` / `checked_out` / `unavailable`; the other enum values are left in place, unused.

**`categories`.** One addition, `sort_order integer not null default 0` (`20260913090000_category_sort_order.sql`). It is a row's position among its *siblings*, ascending, with the name breaking a tie, and it exists because the browse screen sorts by `Catagories.md`'s document order rather than alphabetically (§13, 2026-09-12) and nothing in the table recorded that order. Gaps and duplicates are harmless; the numbers never have to restart at 1 under a parent. Phase 5's category CRUD maintains it. Otherwise unchanged: used as a strict 3-level tree via `parent_id`:
`Type` (e.g. Lenses) → `Category` (e.g. Zooms) → `Subcategory / Model` (e.g. Canon 70-200mm f/2.8). Each physical unit is an `asset` whose `category_id` points at a Model node. Seeded from `Catagories.md`, whose Type and Model names are used verbatim. That file names the Categories under `Lenses` (Zooms, Primes, Accessories) and `Cameras/Bodies` (Camera Model) but lists models straight under the other six types, so the seed invents a middle Category there (Lights → Studio Lights + Light Modifiers, Audio Stuff → Wireless Mics + Wired Mics, and so on); those six are the seed's own naming and are safe to rename. Branches may stop short of depth 3 when a Category has no models yet: `Primes` is seeded empty because `Catagories.md` records none in inventory. `categories.name` is unique across the whole table, not per parent, so generic names are worth avoiding.

**Unused in v1 (tables kept, no code written against them).** `locations`, `tags`, `asset_tags`, `bookings`, `saved_filters`, `assets.custom_fields`, `assets.location_id`. `kits` / `kit_items` are used only if Phase 8 happens.

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

**Custodian visibility.** Who currently holds a checked-out item is visible to any signed-in user. Deliberate, decided 2026-09-12: a student being able to find who has the lens they want outweighs withholding it, and there's no separate school privacy officer for this project to seek sign-off from. This applies only to the *current* holder: `GetAsset`, `ListAssets`, and `ScanItem` include it for every actor, so a browse row can say who has the lens without opening anything. What "who" means is the custodian's **name**, their due date and whether they are overdue, not their student number, which is the scan-login key and so stays admin-only (`AssetCustody.forViewer`, 2026-09-13). Past custodians (the full trail) stay admin-only via `GetAssetHistory`; a non-admin's own history is available only through `GetUserHistory`. Enforce this in the Go API, not the UI. The response itself omits history custodian identities for a non-admin actor, since a hidden field is still a `fetch` call away in the web app.

**Login rules**
- **Scan** (student number arrives as a fast keystroke burst + Enter): sign in with no password.
- **Typed** (same number entered by hand): password required, checked against `password_hash` with bcrypt.
- Roster-imported users have `password_hash = null` (a blank hash counts the same). Their first **scan** login prompts them to set a password before continuing; typed login is impossible until then. Creating a user in the admin panel leaves the account in that same state; the admin can set a password afterwards with the reset endpoint.
- Admins can set or reset any user's password from the admin panel. Passwords are 8 to 72 characters everywhere they are set.
- **Failsafe admin.** `.env` holds `ADMIN_STUDENT_NUMBER` + `ADMIN_PASSWORD`. On every server start, that account is ensured to exist with `is_admin = true` and that password. A way back into the admin panel that doesn't depend on any UI. It is never a startup requirement: unset, malformed, or rejected values are logged as warnings and the server starts without a failsafe admin, because a typo in `.env` must not take the whole API down.

**Sessions**
- In-memory session map (`SessionStore`, owned by `DB`). The login response returns the token and also sets it as an HttpOnly `stockroom_session` cookie; requests may send either `Authorization: Bearer <token>` or the cookie. Restarting the server signs everyone out; acceptable.
- Sessions persist until manual logout or an idle timeout (`SESSION_IDLE_MINUTES`, default **5 minutes**, decided 2026-09-12). Every request refreshes the deadline. After a checkout completes, the UI offers a "sign out?" prompt because the closet PC is shared.
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
│   ├── failsafe.go            # EnsureFailsafeAdmin, run on every server start
│   ├── sessions.go            # in-memory SessionStore with idle timeout
│   ├── auth.go                # Actor, RequireAdmin, RequireFullSession, the login/logout/Resolve/Me methods on DB
│   ├── users.go, roster.go    # user CRUD + password reset; roster CSV import
│   ├── categories.go          # categoryTree (the one in-memory shape of the table) + GetCategoryTree
│   ├── categories_admin.go    # category create/update/delete, depth and cycle rules
│   ├── assets.go              # browse: ListAssets, GetAsset, current-holder reads, browse sort
│   ├── assets_admin.go        # asset create/update/delete/status/photo
│   ├── custody.go             # ScanItem, CheckOutAssets, CheckInAsset, the lists and histories
│   ├── photos.go              # staged photo writes, FilesPrefix, photo URLs
│   └── backup.go              # ExportAllTablesToCSV, BackupNow
├── server/                    # net/http JSON API on localhost; handlers decode, call the package, encode
│   ├── main.go, router.go, json.go, session.go, files.go
│   └── auth.go, users.go, assets.go, custody.go, admin.go
├── cmd/backup/                # (Phase 7, not yet written) CLI: export + rclone push
├── uploads/                   # profile + asset photos (gitignored), served at /files/
├── desktop-app/               # Wails app, primary UI; Go side is only a window host. Svelte in frontend/src
├── web-app/                   # Vite + Svelte 5 secondary UI
├── supabase/                  # config.toml, migrations/, seed.sql, tests/ (pgTAP)
├── scripts/                   # start-mac.sh, start-windows.ps1 (untested on Windows), test-all.sh, graphify_fix_extraction.py
├── docs/                      # adr/ (decision records), agents/ (skill notes), design/ (design system)
├── Catagories.md              # source of truth for the initial category tree
├── CONTEXT.md                 # domain glossary
├── CLAUDE.md, TODO.md, README.md, TESTING.md, CI.md
```

`desktop-app/frontend/src/lib/{supabase,db}.ts` still call PostgREST directly; TODO Phase 6 replaces them with `lib/api.ts`.

### 8.1 Endpoints

Every route except `/health`, the two logins and `/files/` needs a session. "Admin" below means `RequireAdmin` inside the package, not the router. A limited session (Section 7) reaches only the three routes marked so.

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
| `GET /users/{id}/history` | own, or admin | |
| `GET /custody/active`, `GET /custody/overdue`, `GET /assets/{id}/history` | admin | |
| `GET/POST /users`, `GET/PUT/DELETE /users/{id}`, `POST /users/{id}/password` | admin | `UserInput` has no `photo_path`; the roster import is the only way a profile gets a photo |
| `POST /users/import` | admin | multipart `file` (+ optional `photo_dir`) or a `text/csv` body |
| `POST /assets`, `PUT/DELETE /assets/{id}`, `POST /assets/{id}/status` | admin | `AssetInput` has no `photo_path` and no status; status takes `{status: available\|unavailable}` |
| `POST /assets/{id}/photo` | admin | multipart `photo` part, 10 MB cap, `.jpg .jpeg .png .gif .webp` only; the file lands at `uploads/assets/<id>.<ext>` and the response is the asset with its new `photo_url` |
| `POST /categories`, `PUT/DELETE /categories/{id}` | admin | `{name, parent_id?, sort_order?}`; depth capped at 3, delete refused with children or assets |
| `POST /admin/backup` | admin | runs the CSV export into `BACKUP_DIR` |
| `GET /files/...` | nobody | photos off `UPLOADS_DIR`; `<img>` tags cannot send a bearer token |

**Statuses.** `ErrNotFound` 404, `ErrInvalid` 400, `ErrUnauthorized`/`ErrBadCredentials`/`ErrPasswordNotSet` 401, `ErrForbidden` 403, `ErrConflict`/`ErrOverdueBlocked` 409, anything else 500 with the detail logged, not sent. **`ErrNotConfigured` is 503** with its message intact: an unset `UPLOADS_DIR` or `BACKUP_DIR` is neither the client's fault nor a bug, and the admin reading the response is the person who edits `.env`.

---

## 9. Setup instructions

**Easiest path.** See `README.md`: `./scripts/start-mac.sh` (macOS) or `scripts/start-windows.ps1` (Windows). Ctrl+C stops everything and preserves data.

### Manual steps
1. Install Go, Node.js, Docker Desktop, the Supabase CLI, and the Wails CLI (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`).
2. Copy `.env.example` to `.env` and fill in:

   | Var | Purpose | Local default |
   |---|---|---|
   | `DATABASE_URL` | direct Postgres connection | `postgresql://postgres:postgres@127.0.0.1:54322/postgres` |
   | `SERVER_ADDR` | Go server listen address | `127.0.0.1:8080` |
   | `ADMIN_STUDENT_NUMBER` | failsafe admin account (Section 7); digits only | (none) |
   | `ADMIN_PASSWORD` | failsafe admin password; at least 8 characters | (none) |
   | `UPLOADS_DIR` | where photos are copied | `./uploads` |
   | `BACKUP_DIR` | local CSV export target, also `rclone`'s source folder | (none) |
   | `RCLONE_REMOTE` | `rclone` remote name (set up via `rclone config`) that `cmd/backup` copies `BACKUP_DIR` to | (none) |
   | `SESSION_IDLE_MINUTES` | idle timeout | `5` |

3. `supabase start` (repo root). Postgres + Studio (`http://127.0.0.1:54323`); migrations + seed apply automatically. The seed creates an admin (student number `100001`, typed-login password `stockroom`) and a student (`200001`, no password yet).
4. `go run ./server`. The API. Check `curl http://127.0.0.1:8080/health`.
5. `cd desktop-app && wails dev`. Primary UI, native window.
6. `cd web-app && npm run dev`. Secondary UI on `http://localhost:5173`.
7. `supabase stop`. Stops the stack, preserves data.

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

Nightly, a Go CLI (`cmd/backup`) exports every table to CSV into `BACKUP_DIR/<yyyy-mm-dd>/<table>.csv`, then shells out to `rclone copy BACKUP_DIR <remote>:` to push that same folder to Google Drive (decided 2026-09-12, replacing the earlier local-only/Drive-client-syncs-it plan). `rclone` is configured once, interactively (`rclone config`), on whichever machine runs the backup; the stored remote name goes in `.env` alongside `BACKUP_DIR`. The local CSVs are kept regardless of upload success, both as a fallback and because the restore test below reads from them directly. Scheduling: Windows Task Scheduler on the closet PC; launchd or cron on macOS for dev. The same export function is exposed as "Backup Now" in the admin panel.

Before go-live, test a full restore: wipe a scratch database, reapply migrations, reload from CSV, confirm row counts match.

---

## 12. Build timeline (9 weeks)

**Weeks 1 to 2 (done).** Environment prep, tooling, planning.

**Week 3 (done).** Chose Supabase-hosted Postgres. Schema migration written and applied; all tables/views/enums verified.

**Week 4 (done).** Supabase stack healthy; seed data; proved the chain end-to-end with a temporary supabase-js admin screen in the Wails app (asset + tag CRUD). Fixed two scaffold bugs: `vite@^8` → `^7` for `@sveltejs/vite-plugin-svelte@6`, and Svelte 5 needs `mount(App, …)` not `new App(…)` (blank window, no error). Wrote README + one-step start scripts (`start-mac.sh` verified; `start-windows.ps1` untested). Interviewed and pinned down the product flow, auth model, and Go-backend architecture (this document).

**Week 5: Go foundation + schema + auth** (TODO Phases 0 to 2)
Root `go.mod`, `internal/stockroom` + `server/` skeleton, pgx connection, `.env`. v1 migration (Section 6.2) and category seed. Scan/typed login, sessions, `RequireAdmin`, failsafe admin, roster CSV import, user CRUD. Start scripts launch the server on both OSes.

**Week 6: Browse + core loop** (TODO Phases 3 to 4)
Asset list with category-tree filters, item detail, `ScanItem`, bulk cart checkout with 7-day due cap and overdue block, check-in with damage note, custody history, overdue list. **All core logic finalized by the end of this week.**

**Week 7: Admin panel + real data + scanner** (TODO Phase 5, hardware)
Admin panel endpoints (asset/category/user management, overdue, Backup Now). Buy the barcode scanner, tune scan-vs-typed detection against it. Print serial stickers; begin real inventory entry (recruit a CS class / volunteers).

**Week 8: UI + frontend wiring + backup** (TODO Phases 6 to 7)
Build the screens in the Wails app first, then mirror in the web app, both on `lib/api.ts`. Delete the supabase-js path. Backup CLI + scheduling + restore test.

**Week 9: Kits if time, then testing + presentation** (TODO Phase 8)
Kits only if everything above is solid. Final testing, walkthrough prep, presentation.

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
- [x] Out of scope: bookings, locations, tags, saved filters, custom fields, LAN access, email. Kits = lowest priority.
- [x] Backup: nightly CSV via Go CLI into a **Google Drive** folder.
- [x] Styling (2026-09-05): **Tailwind CSS v4** in both frontends via `@tailwindcss/vite`; design tokens live in each app's `src/app.css` `@theme` block (desktop: the dark "Nocturne" system from the UI import). No component CSS files, no `tailwind.config.js`.

- [x] Failsafe admin is best-effort (2026-09-08): `EnsureFailsafeAdmin` returns `ErrFailsafeNotConfigured` when the `.env` values are blank, and the server only ever logs a warning. Nothing about the failsafe can stop the API from starting.
- [x] Student numbers (2026-09-08): stored as digits-only text, no fixed length. Cards encode six digits, but `NormalizeStudentNumber` accepts 1 to 32 so a reissued or imported number still works; the bound is a mis-scan guard, not a format.

**Closed (2026-09-12 grilling session; also resolves `docs/design/design-system.md` §15 Q1 to Q7)**
- [x] Session idle timeout: **5 minutes** (`SESSION_IDLE_MINUTES` default).
- [x] Machine operation model: **unattended student self-service** is the primary path; admin check-out-on-behalf-of stays the rare override it was already specced as.
- [x] Cart never mixes borrowing and returning: scanning a checked-out item checks it in immediately and never touches the cart; the cart only ever accumulates items being borrowed.
- [x] Scanning an item barcode with nobody signed in shows an explicit "sign in first" message rather than trying to interpret the code as a student number.
- [x] The frontend cart clears only on sign-out or the idle timeout, **not** on a page reload.
- [x] Overdue block is enforced at **both** layers: the checkout UI disables itself the moment an overdue user signs in, and `CheckOutAssets` also refuses server-side regardless of what the client sends.
- [x] **Custodian visibility, reversing the 2026-09-09 review tightening**: who currently holds a checked-out item is visible to any signed-in user, not admin-only. Applies only to the current holder. `GetAssetHistory`'s full past-custodian trail stays admin-only, and a non-admin's own history is available only via `GetUserHistory`. See §7.
- [x] Browse list sort order (previously unspecified): categories in `Catagories.md`'s document order, not alphabetical; within any list, available units sort before checked-out ones.
- [x] Backup: nightly CSV → local folder → **`rclone copy` pushes it to Google Drive** directly, replacing the "Drive desktop client syncs a local folder, no cloud API code" plan. One-time interactive `rclone config` OAuth setup instead of hand-written Google API/OAuth code. See §11.

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

**Still open**
- [ ] Barcode scanner model (Week 7). Must be plain HID keyboard-wedge
- [ ] Scan-vs-typed keystroke threshold. Ships as a named/configurable constant defaulted to 50ms; tune with real hardware in Week 7
- [ ] Exact `BACKUP_DIR` path and `rclone` remote name on the closet PC (blocked on the PC being provisioned)

---

## Agent skills

### Issue tracker

Issues live in GitHub Issues for `Kathir-D/Stockroom` (via the `gh` CLI). See `docs/agents/issue-tracker.md`.

### Triage labels

Default vocabulary: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `CONTEXT.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.

### Knowledge graph (graphify)

`graphify-out/` holds a graphify knowledge graph of the repo (`graph.json`, `graph.html`, `GRAPH_REPORT.md`). It is gitignored: the files are regenerated wholesale on every rebuild and carry machine-specific paths, so each machine builds its own. Stock graphify extracts this repo badly in four ways, so rebuild through `scripts/graphify_fix_extraction.py` instead of a plain `/graphify` or `/graphify --update`:

- **Svelte.** Stock graphify parses the whole `.svelte` file with the JavaScript grammar, so the markup and `lang="ts"` annotations fail and most script symbols are lost. The script blanks everything outside `<script>` (line numbers kept) and parses the rest as TypeScript.
- **Dangling edges.** Imports of `stockroom/internal/stockroom` are pointed at a real package node that `contains` the package's files. Imports of external packages (Go stdlib, npm) have no node, so those edges are dropped and listed under `external_imports` on the importing file's node.
- **Self-loops.** A file node that `contains` itself is dropped. The two SQL self-loops (`locations.parent_id`, `categories.parent_id`) are real foreign keys to their own table and stay.
- **Collapsed edges.** graphify keeps one edge per node pair, so repeats were silently lost (e.g. `Asset`'s four `time.Time` fields). They are merged into one edge with `weight` = count and every line kept in `source_locations`.

The script writes `graphify-out/.graphify_detect.json` and `.graphify_extract.json`; after it, run the normal graphify build, cluster, label, report, and `graphify export html` steps. It reuses graphify's semantic cache for docs and images and warns if any are uncached; those need a full `/graphify` run first. It needs `tree_sitter_sql` installed (`pip install "graphifyy[sql]"`) or the `.sql` files are skipped.

```bash
$(cat graphify-out/.graphify_python) scripts/graphify_fix_extraction.py
```

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
