# Stockroom — Media Department Asset Checkout System

This file is the master reference for the project: what it is, how it's built, how to set it up, and the build timeline. Keep it updated as decisions get made — it's meant to be the single source of truth for anyone (human or AI) picking up this codebase.

---

## 1. What this is

Stockroom is a fully local asset checkout/check-in system for the school's media department. It replaces manual equipment tracking with barcode-driven scanning, custody tracking, and reservations — all running on a single dedicated machine, with no internet/wifi dependency for daily use.

**Core loop:** scan a barcode → see the asset → check it out to a person or check it back in. Everything else (bookings, kits, roles, custom fields) supports that loop.

---

## 2. Goals / Non-goals

**Goals**
- Know what equipment exists, where it is, and who has it
- Prevent double-booking of equipment
- Check items in/out in seconds via barcode scan
- Full history/audit trail on every asset
- Zero dependency on internet for core daily operation
- Automated off-site backup (CSV, via cloud-synced folder)

**Non-goals (for MVP)**
- Multi-station simultaneous access (architecture allows adding later; not built now)
- Native mobile apps
- Email/SMS reminders (needs internet — deferred; in-app alerts only for now)
- GPS auto-tagging (manual lat/long only)
- Kits, custom field editor, saved filter presets, CSV export/reporting polish (fast-follow after MVP)

---

## 3. Deployment model

One dedicated Windows PC lives in the camera closet, always on. It hosts the database and backend. The barcode scanner plugs into this machine. Two front ends talk to the same local backend:

- **Wails desktop app** (`desktop-app/`) — primary interface, native window (Svelte + TypeScript), scanner-driven
- **Web app** (`web-app/`) — secondary interface, served on `localhost` (Vite + Svelte + TypeScript, NOT Astro — that was the original plan, changed during scaffolding), mirrors the desktop app

Nightly, a script exports every table to CSV into a folder watched by a cloud sync client (Google Drive/OneDrive/Dropbox) already installed on the machine — the sync client handles the actual off-site upload, no custom cloud API code needed.

---

## 4. Backend architecture — DECIDED: Option A (Supabase)

Running Postgres + Auth (GoTrue) + REST API (PostgREST) + Studio admin UI locally via the Supabase CLI (`supabase start`, Docker-based). This was chosen over the Docker-free native-Postgres alternative for simplicity, since the dev/deployment target is a single dedicated workstation where Docker's extra moving part is an acceptable tradeoff for getting Auth + Studio out of the box.

**Auth model:** both frontends talk to the local Supabase REST API using the **service_role key** (not the anon key), which bypasses Row Level Security entirely. `anon`/`authenticated` roles have been explicitly revoked from all tables (see `supabase/migrations/20260826180000_grant_service_role.sql`). This is intentional, not an oversight — RLS policies are unneeded complexity for a single trusted local machine with no untrusted network exposure. **Role permission checks (owner/executive_producer/producer/member, Section 7) must be enforced in app code**, since there is no RLS layer doing it automatically.

---

## 5. Tech stack summary

| Layer | Technology |
|---|---|
| Database | PostgreSQL (via Supabase local stack) |
| REST API | Supabase's bundled PostgREST, accessed with the service_role key (no RLS) |
| Auth | None — service_role key bypasses auth; role checks done in app code |
| Desktop app | Wails (Go backend + Svelte 5 + TypeScript frontend) |
| Web app | Vite + Svelte 5 + TypeScript (not Astro — plan changed during scaffolding) |
| Barcode scanner | Standard USB HID scanner (keyboard-wedge — no drivers) — not yet tested with real hardware |
| Backups | Scheduled script → CSV → cloud-synced folder (Windows Task Scheduler) — not yet built |

---

## 6. Database schema

This is the schema as originally designed. It has been applied and verified working — see `supabase/migrations/20260826173006_init_schema.sql` for the actual migration (identical to below), plus a follow-up migration `supabase/migrations/20260826180000_grant_service_role.sql` that grants full privileges to `service_role` and revokes `anon`/`authenticated` (see Section 4). Sample data lives in `supabase/seed.sql` (10 sample assets across 5 categories, 2 locations, 1 test profile) and loads automatically on `supabase db reset`.

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
  password_hash text,          -- only used under Option B; omit if using Supabase Auth
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

> RLS is intentionally **not** enabled (see Section 4 — service_role key bypasses it). Role checks happen in app code instead.

---

## 7. Roles

| Role | Typical permissions |
|---|---|
| `owner` | Full access — assets, users, roles, deletion |
| `executive_producer` | Full access except deleting other users |
| `producer` | Create/edit assets, process checkouts/checkins, manage bookings |
| `member` | View assets, create their own bookings, view their own custody history |

---

## 8. Repository structure

```
stockroom/
├── desktop-app/               # Wails app — primary UI
│   ├── app.go
│   ├── main.go
│   ├── wails.json
│   └── frontend/
│       └── src/
│           ├── lib/supabase.ts   # Supabase client (service_role key)
│           ├── App.svelte        # assets list screen (proof of chain: auth → API → render)
│           └── main.ts
├── web-app/                   # Vite + Svelte secondary UI — scaffolded only, not wired to Supabase yet
│   └── src/
├── supabase/
│   ├── config.toml
│   ├── migrations/
│   │   ├── 20260826173006_init_schema.sql      # full schema (Section 6)
│   │   └── 20260826180000_grant_service_role.sql
│   └── seed.sql                # sample assets/locations/categories, loads on `supabase db reset`
├── scripts/
│   ├── start-mac.sh            # one-step start/stop for macOS, verified working end-to-end
│   └── start-windows.ps1       # Windows equivalent — written but NOT yet tested on real Windows
├── CLAUDE.md                   # this file
└── README.md                  # dependencies + how to run
```

---

## 9. Setup instructions

**Easiest path:** see `README.md` — run `./scripts/start-mac.sh` (macOS) or `scripts/start-windows.ps1` (Windows). It checks dependencies, starts Docker + the local Supabase stack, installs frontend packages, and launches both frontends. Ctrl+C stops everything cleanly and preserves data.

### Manual steps
1. Install Go, Node.js, Docker Desktop, the Supabase CLI, and the Wails CLI (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`) — see README for download links
2. `supabase start` (from repo root) — starts Postgres, Auth, REST API, Studio (`http://127.0.0.1:54323`); migrations + seed data apply automatically
3. `cd desktop-app && wails dev` — primary UI, opens a native window
4. `cd web-app && npm run dev` — secondary UI (not yet wired to Supabase)
5. `supabase stop` — stops the stack, preserves data (add `--no-backup` only if you intentionally want to discard it)

---

## 10. Barcode scanner notes

USB barcode scanners act as HID keyboard-wedge devices — they type the scanned code followed by Enter into whatever field has focus. No SDK or driver integration needed. Implementation pattern for both UIs:

```js
let buffer = '';
window.addEventListener('keydown', (e) => {
  if (e.key === 'Enter') {
    lookupAsset(buffer);
    buffer = '';
  } else {
    buffer += e.key;
  }
});
```
Keep a visible input focused so manual typing works identically as a fallback.

---

## 11. Backup strategy

Nightly, scheduled via Windows Task Scheduler, a script dumps every core table to CSV into a folder watched by a cloud sync client already installed on the machine (Google Drive/OneDrive/Dropbox):

```powershell
$dumpDir = "C:\Users\<user>\Google Drive\StockroomBackups\$(Get-Date -Format yyyy-MM-dd)"
New-Item -ItemType Directory -Path $dumpDir -Force
$tables = @("assets","bookings","custody_events","activity_log","locations","categories","kits")
foreach ($t in $tables) {
  psql "postgresql://<connection-string>" -c "\copy $t TO '$dumpDir\$t.csv' WITH CSV HEADER"
}
```
Test a full restore at least once before go-live: wipe a scratch database, reapply the migration, reload from CSV, confirm row counts match.

---

## 12. Build timeline (9 weeks total)

**Weeks 1–2 (complete)** — Environment prep, tooling installed, initial planning.

**Week 3 (complete)** — Decided Option A (Supabase). `supabase init` run, full schema migration written and applied, verified all 12 tables + 2 views + 4 enums present via direct psql inspection and via the REST API.

**Week 4 (in progress) — Database connectivity**
Done so far:
- Supabase local stack confirmed fully healthy (all containers up, REST API reachable)
- Added `grant_service_role` migration (Section 4) after discovering the initial schema migration never granted table privileges to any Postgres role, including `service_role`
- Seeded 10 sample assets across 5 categories + 2 locations (`supabase/seed.sql`)
- Wired the Wails desktop frontend to Supabase (`@supabase/supabase-js`, service_role key) and built a working assets-list screen — verified rendering real data end-to-end in a live view of the running app
- Fixed two pre-existing scaffold bugs blocking `wails dev`: `desktop-app/frontend/package.json` pinned an incompatible `vite@^8` (needs `^7` for `@sveltejs/vite-plugin-svelte@6`), and `main.ts` used the Svelte 4 `new App(...)` API against the installed Svelte 5 (needs `mount(App, {...})` from `'svelte'`) — this second one silently produced a blank window with no console error, worth remembering if the web-app hits the same issue
- Wrote `README.md` and one-step start/stop scripts (`scripts/start-mac.sh`, tested and working; `scripts/start-windows.ps1`, written but **not yet tested on a real Windows machine** — verify tree-kill via `taskkill /T /F` actually cleans up `wails dev`'s child processes before relying on it)

Still open for Week 4:
- Connect Go (Wails backend, `app.go`) to Supabase directly for mutations — everything so far is read-only from the Svelte frontend via `supabase-js`; no create/update/delete flow built yet
- Wire the web-app (`web-app/`) to Supabase — currently just the Vite/Svelte scaffold, no client, no screens

**Week 5 — Frontend + accounts + check-in/out (start)**
Rough out frontend formatting/layout direction. Begin the account system (login, role assignment). Start building the check-in/check-out flow.

**Week 6 — Finish core logic**
Complete all logic for check-in/check-out, accounts, and database mutations. Confirm every system works together correctly. **All core logic should be finalized by the end of this week.**

**Week 7 — Real-world data + scanner**
Buy and integrate the barcode scanner as the primary lookup/sign-in method. Begin real-world data entry (consider recruiting a CS class or student volunteers to help catalog and enter existing equipment). If time allows, start planning frontend visual design.

**Week 8 — Build the UI**
Build the desktop UI in Wails first. Once complete, rebuild the same UI in Astro for the local web version. After both are done, polish and fix issues found during testing.

**Week 9 — Presentation**
Final testing, walkthrough prep, and presentation.

---

## 13. Open decisions

- [x] Option A vs. Option B backend → **Option A, Supabase** (Section 4)
- [x] Svelte vs. React → **Svelte 5** (both `desktop-app` and `web-app` scaffolded with it)
- [x] RLS vs. app-code role enforcement → **app-code**, service_role key bypasses RLS (Section 4)
- [ ] Which cloud sync client is already installed on the closet PC (Drive/OneDrive/Dropbox) — determines the backup folder path
- [ ] Barcode scanner model to purchase (Week 7) — confirm it's a standard HID keyboard-wedge scanner, not one requiring proprietary software
- [ ] Web-app framework note: CLAUDE.md originally called for Astro; it was scaffolded as plain Vite+Svelte instead. Decide whether to migrate to Astro later or keep Vite+Svelte as the permanent choice (Section 3/5 currently reflect Vite+Svelte as the actual state)
