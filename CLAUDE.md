# Stockroom: media department asset checkout system

This file is the master reference for the project: what it is, how it's built, how to set it up, and the build timeline. Keep it updated as decisions get made. It's meant to be the single source of truth for anyone (human or AI) picking up this codebase. `TODO.md` tracks the phase-by-phase backend work; this file explains the *why* and the *shape*.

Last major revision: 2026-09-04 (product flow, auth model, and backend architecture all pinned down; see Section 13 for what changed).

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
- Automated nightly off-site backup (CSV into a Google Drive-synced folder)

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

The USB barcode scanner plugs into this machine. Nightly, a Go CLI exports every table to CSV into a folder that the Google Drive client (already installed on the PC) syncs off-site. No cloud API code needed.

---

## 4. Backend architecture (decided): single Go backend over Supabase-hosted Postgres

**The Go server is the only database client.** `internal/stockroom` holds every query, transaction, permission check, and account operation. `server/` wraps it in HTTP handlers. Both frontends call those endpoints with `fetch` through a small `lib/api.ts` wrapper; no Supabase JS client, no PostgREST, no database credentials in TypeScript.

Why this shape:
- One place for all logic, written in Go (preferred over TS for this codebase).
- Both UIs share one code path and one session store, so behaviour can't drift.
- Postgres still runs inside the Supabase CLI stack because migrations, seed loading, and Studio are already set up and working. Go connects to the **direct Postgres port (54322)** via `DATABASE_URL`.

**Historical note.** An earlier iteration had the Svelte frontend calling PostgREST directly with the `service_role` key (see `supabase/migrations/20260826180000_grant_service_role.sql` and `desktop-app/frontend/src/lib/supabase.ts` / `db.ts`). That migration is harmless and stays; the TS files are slated for deletion in TODO Phase 6. RLS is still not enabled and doesn't need to be. Nothing but the Go server (connecting as `postgres`) reaches the DB.

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
| Backup | Go CLI (`cmd/backup`) → CSV per table → Google Drive-synced folder; scheduled by Task Scheduler (Windows) / launchd or cron (macOS) |
| Config | `.env` at repo root (see Section 9) |

---

## 6. Database schema

### 6.1 Base schema (applied)

Applied via `supabase/migrations/20260826173006_init_schema.sql` and verified working (12 tables, 2 views, 4 enums). Followed by `20260826180000_grant_service_role.sql` (historical, see Section 4) and `20260908100000_v1_flow.sql` (Section 6.2, applied). Sample data in `supabase/seed.sql` loads on `supabase db reset`.

```sql
create extension if not exists "uuid-ossp";
create extension if not exists "btree_gist";

create type user_role as enum ('owner', 'executive_producer', 'producer', 'member');
create type asset_status as enum ('available', 'checked_out', 'reserved', 'maintenance', 'retired', 'lost');
create type booking_status as enum ('reserved', 'active', 'returned', 'overdue', 'cancelled');
create type location_type as enum ('building', 'floor', 'room', 'shelf', 'other');

create table profiles (
  id uuid primary key default uuid_generate_v4(),
  email text not null unique,
  password_hash text,
  full_name text,
  role user_role not null default 'member',
  created_at timestamptz not null default now()
);

create table locations (
  id uuid primary key default uuid_generate_v4(),
  name text not null,
  type location_type not null default 'other',
  parent_id uuid references locations(id) on delete set null,
  gps_lat double precision,
  gps_lng double precision,
  created_at timestamptz not null default now()
);

create table categories (
  id uuid primary key default uuid_generate_v4(),
  name text not null unique,
  parent_id uuid references categories(id) on delete set null,
  created_at timestamptz not null default now()
);

create table tags (
  id uuid primary key default uuid_generate_v4(),
  name text not null unique
);

create table assets (
  id uuid primary key default uuid_generate_v4(),
  asset_tag text not null unique,
  name text not null,
  description text,
  category_id uuid references categories(id) on delete set null,
  location_id uuid references locations(id) on delete set null,
  status asset_status not null default 'available',
  condition text,
  serial_number text,
  purchase_date date,
  purchase_price numeric(10,2),
  warranty_expiration date,
  custom_fields jsonb not null default '{}'::jsonb,
  created_by uuid references profiles(id),
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create index idx_assets_status on assets(status);
create index idx_assets_category on assets(category_id);
create index idx_assets_location on assets(location_id);
create index idx_assets_custom_fields on assets using gin(custom_fields);
create index idx_assets_search on assets using gin (
  to_tsvector('english', coalesce(name,'') || ' ' || coalesce(description,'') || ' ' || coalesce(serial_number,''))
);

create or replace function set_updated_at() returns trigger as $$
begin
  new.updated_at = now();
  return new;
end;
$$ language plpgsql;

create trigger trg_assets_updated_at
before update on assets
for each row execute function set_updated_at();

create table asset_tags (
  asset_id uuid references assets(id) on delete cascade,
  tag_id uuid references tags(id) on delete cascade,
  primary key (asset_id, tag_id)
);

create table kits (
  id uuid primary key default uuid_generate_v4(),
  name text not null,
  description text,
  created_at timestamptz not null default now()
);

create table kit_items (
  kit_id uuid references kits(id) on delete cascade,
  asset_id uuid references assets(id) on delete cascade,
  primary key (kit_id, asset_id)
);

create table bookings (
  id uuid primary key default uuid_generate_v4(),
  asset_id uuid references assets(id) on delete cascade,
  kit_id uuid references kits(id) on delete cascade,
  reserved_by uuid not null references profiles(id),
  start_date timestamptz not null,
  end_date timestamptz not null,
  status booking_status not null default 'reserved',
  notes text,
  created_at timestamptz not null default now(),
  check ((asset_id is not null and kit_id is null) or (asset_id is null and kit_id is not null)),
  check (end_date > start_date),
  exclude using gist (
    asset_id with =,
    tstzrange(start_date, end_date) with &&
  ) where (status in ('reserved', 'active') and asset_id is not null)
);

create index idx_bookings_asset on bookings(asset_id);
create index idx_bookings_kit on bookings(kit_id);
create index idx_bookings_dates on bookings(start_date, end_date);

create table custody_events (
  id uuid primary key default uuid_generate_v4(),
  asset_id uuid not null references assets(id) on delete cascade,
  booking_id uuid references bookings(id) on delete set null,
  custodian_id uuid not null references profiles(id),
  checked_out_by uuid not null references profiles(id),
  checked_out_at timestamptz not null default now(),
  due_at timestamptz,
  checked_in_at timestamptz,
  checked_in_by uuid references profiles(id),
  condition_out text,
  condition_in text,
  notes text
);

create index idx_custody_asset on custody_events(asset_id);
create index idx_custody_open on custody_events(asset_id) where checked_in_at is null;

create table activity_log (
  id uuid primary key default uuid_generate_v4(),
  asset_id uuid references assets(id) on delete cascade,
  actor_id uuid references profiles(id),
  action text not null,
  details jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default now()
);

create index idx_activity_asset on activity_log(asset_id, created_at desc);

create or replace function log_asset_status_change() returns trigger as $$
begin
  if TG_OP = 'UPDATE' and old.status is distinct from new.status then
    insert into activity_log (asset_id, actor_id, action, details)
    values (new.id, null, 'status_change', jsonb_build_object('from', old.status, 'to', new.status));
  end if;
  return new;
end;
$$ language plpgsql;

create trigger trg_asset_status_log
after update on assets
for each row execute function log_asset_status_change();

create table saved_filters (
  id uuid primary key default uuid_generate_v4(),
  user_id uuid not null references profiles(id) on delete cascade,
  name text not null,
  filter_json jsonb not null,
  created_at timestamptz not null default now()
);

create view active_custody as
select ce.*, a.name as asset_name, a.asset_tag
from custody_events ce
join assets a on a.id = ce.asset_id
where ce.checked_in_at is null;

create view overdue_custody as
select * from active_custody
where due_at is not null and due_at < now();
```

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

**`categories`.** No change. Used as a strict 3-level tree via `parent_id`:
`Type` (e.g. Lenses) → `Category` (e.g. Zooms) → `Subcategory / Model` (e.g. Canon 70-200mm f/2.8). Each physical unit is an `asset` whose `category_id` points at a Model node. Seeded from `Catagories.md`; where that file lists models straight under a type, the seed inserts a middle Category (Lights → Studio Lights, Audio Stuff → Wired Mics, and so on) so every branch is three deep. `categories.name` is unique across the whole table, not per parent.

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
| View own custody history | ✓ | ✓ |
| Admin panel: asset CRUD, mark unavailable, category tree CRUD | | ✓ |
| Admin panel: user CRUD, roster CSV import, set/reset any password | | ✓ |
| Admin panel: overdue list, override overdue-block on checkout, Backup Now | | ✓ |

**Login rules**
- **Scan** (student number arrives as a fast keystroke burst + Enter): sign in with no password.
- **Typed** (same number entered by hand): password required, checked against `password_hash` with bcrypt.
- Roster-imported users have `password_hash = null`. Their first **scan** login prompts them to set a password before continuing; typed login is impossible until then.
- Admins can set or reset any user's password from the admin panel.
- **Failsafe admin.** `.env` holds `ADMIN_STUDENT_NUMBER` + `ADMIN_PASSWORD`. On every server start, that account is ensured to exist with `is_admin = true` and that password. A way back into the admin panel that doesn't depend on any UI.

**Sessions**
- In-memory session map in the Go server (token in a cookie or `Authorization` header). Restarting the server signs everyone out; acceptable.
- Sessions persist until manual logout or an idle timeout (length TBD, Section 13). After a checkout completes, the UI offers a "sign out?" prompt because the closet PC is shared.

**Overdue rule**
- Signing in with any overdue item shows a warning. Attempting a checkout while overdue is refused by the server; an admin can override per checkout.

---

## 8. Repository structure (target)

```
stockroom/
├── go.mod                     # single root module; desktop-app, server, cmd share internal/
├── .env.example               # copy to .env, see Section 9
├── internal/stockroom/        # ALL business logic; the only code that touches Postgres
│   ├── db.go                  # pgxpool setup
│   ├── types.go               # structs for every table + views
│   ├── errors.go              # ErrNotFound, ErrForbidden, ErrConflict, ErrOverdueBlocked, ...
│   ├── password.go            # bcrypt helpers, student-number validation
│   ├── failsafe.go            # EnsureFailsafeAdmin (run on every server start)
│   ├── auth.go                # LoginByScan / LoginByPassword / sessions / RequireAdmin
│   ├── assets.go, categories.go, users.go, custody.go, backup.go, ...
├── server/                    # Go net/http JSON API on localhost; thin handlers over internal/stockroom
│   └── main.go
├── cmd/backup/                # CLI: export all tables to CSV (Task Scheduler / launchd)
│   └── main.go
├── uploads/                   # profile + asset photos (gitignored), served at /files/
├── desktop-app/               # Wails app, primary UI; Go side is only a window host
│   ├── app.go, main.go, wails.json
│   └── frontend/src/
│       ├── lib/api.ts         # fetch wrappers over server/ endpoints (replaces supabase.ts + db.ts)
│       ├── lib/scanner.ts     # keystroke buffer + scan-vs-typed detection (Section 10)
│       └── ...screens
├── web-app/                   # Vite + Svelte 5 secondary UI; same lib/api.ts pattern
│   └── src/
├── supabase/
│   ├── config.toml
│   ├── migrations/
│   │   ├── 20260826173006_init_schema.sql
│   │   ├── 20260826180000_grant_service_role.sql   # historical, harmless
│   │   └── 20260908100000_v1_flow.sql              # Section 6.2
│   └── seed.sql               # category tree from Catagories.md + sample assets + failsafe admin
├── scripts/
│   ├── start-mac.sh           # start/stop everything on macOS (needs: also launch server/)
│   └── start-windows.ps1      # Windows equivalent, untested on real Windows
├── Catagories.md              # source of truth for the initial category tree
├── CLAUDE.md                  # this file
├── TODO.md                    # phase-by-phase backend work
└── README.md                  # dependencies + how to run
```

Current state differs: `cmd/` doesn't exist yet, and `desktop-app/frontend/src/lib/{supabase,db}.ts` still call PostgREST directly. TODO Phase 6 and Phase 7 close that gap.

---

## 9. Setup instructions

**Easiest path.** See `README.md`: `./scripts/start-mac.sh` (macOS) or `scripts/start-windows.ps1` (Windows). Ctrl+C stops everything and preserves data. (Both scripts still need to be updated to launch the Go server; TODO Phase 0.)

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
   | `BACKUP_DIR` | CSV export target (Google Drive-synced folder) | (none) |
   | `SESSION_IDLE_MINUTES` | idle timeout | TBD |

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

Nightly, a Go CLI (`cmd/backup`) exports every table to CSV into `BACKUP_DIR/<yyyy-mm-dd>/<table>.csv`. `BACKUP_DIR` points inside the Google Drive folder on the closet PC, so the already-installed Drive client uploads it off-site. Scheduling: Windows Task Scheduler on the closet PC; launchd or cron on macOS for dev. The same export function is exposed as "Backup Now" in the admin panel.

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

**Still open**
- [ ] Barcode scanner model (Week 7). Must be plain HID keyboard-wedge
- [ ] Session idle-timeout length (`SESSION_IDLE_MINUTES`)
- [ ] Scan-vs-typed keystroke threshold. Tune with real hardware
- [ ] Exact `BACKUP_DIR` path on the closet PC

---

## Agent skills

### Issue tracker

Issues live in GitHub Issues for `Kathir-D/Stockroom` (via the `gh` CLI). See `docs/agents/issue-tracker.md`.

### Triage labels

Default vocabulary: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `CONTEXT.md` and `docs/adr/` at the repo root. See `docs/agents/domain.md`.
