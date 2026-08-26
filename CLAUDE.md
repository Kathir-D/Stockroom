# Trove — Media Department Asset Checkout System

This file is the master reference for the project: what it is, how it's built, how to set it up, and the build timeline. Keep it updated as decisions get made — it's meant to be the single source of truth for anyone (human or AI) picking up this codebase.

---

## 1. What this is

Trove is a fully local asset checkout/check-in system for the school's media department. It replaces manual equipment tracking with barcode-driven scanning, custody tracking, and reservations — all running on a single dedicated machine, with no internet/wifi dependency for daily use.

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

- **Wails desktop app** — primary interface, native window, scanner-driven
- **Astro web app** — secondary interface, served on `localhost`, mirrors the desktop app

Nightly, a script exports every table to CSV into a folder watched by a cloud sync client (Google Drive/OneDrive/Dropbox) already installed on the machine — the sync client handles the actual off-site upload, no custom cloud API code needed.

---

## 4. Backend architecture — DECISION POINT (Week 3)

There are two viable paths. Pick one in Week 3 and delete the other section below once decided.

### Option A — Supabase (via Supabase CLI, Docker-based)
Runs Postgres + Auth (GoTrue) + REST API (PostgREST) + Studio admin UI, all bundled via `supabase start`.
- ✅ Auth, admin GUI, and REST API all work out of the box
- ❌ Requires Docker Desktop + WSL2 on the closet PC — needs admin rights, another service that must stay running

### Option B — Native Postgres + PostgREST (recommended, no Docker)
Real Postgres installed as a native Windows service, PostgREST as a single standalone binary providing the same REST API shape, simple hand-rolled login (a `profiles` table + basic password check — genuinely sufficient for one local machine and four roles).
- ✅ No Docker dependency at all — fewer moving parts, less that can break or get blocked by IT policy
- ❌ No Studio-style admin GUI out of the box (use a lightweight tool like DBeaver/pgAdmin instead); auth is hand-rolled instead of a full auth service

**Recommendation:** Option B, given a single dedicated machine with no multi-station requirement — Docker's main benefit (bundling many services) matters least here, and its main cost (another failure point / IT friction) matters most on a tight timeline.

Either way, **the database schema in Section 6 is identical** — only how you stand up Postgres and the REST layer changes.

---

## 5. Tech stack summary

| Layer | Technology |
|---|---|
| Database | PostgreSQL |
| REST API | PostgREST (Option B) or Supabase's bundled PostgREST (Option A) |
| Auth | Hand-rolled (Option B) or Supabase Auth/GoTrue (Option A) |
| Desktop app | Wails (Go backend + Svelte or React frontend) |
| Web app | Astro (same Svelte/React components as islands where interactive) |
| Barcode scanner | Standard USB HID scanner (keyboard-wedge — no drivers) |
| Backups | Scheduled script → CSV → cloud-synced folder (Windows Task Scheduler) |

---

## 6. Database schema

Full schema — apply as a single migration regardless of which backend option you pick.

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

> If you go with **Option A (Supabase)**, also enable Row Level Security and add policies per role (see earlier project discussion for the full policy set). If you go with **Option B**, enforce role checks in the PostgREST layer or in app code instead, since there's no RLS/auth service doing it for you automatically.

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
trove/
├── db/
│   └── migrations/
│       └── 001_init_schema.sql
├── desktop-app/          # Wails app — primary UI
│   ├── app.go
│   ├── main.go
│   └── frontend/
├── web-app/              # Astro app — secondary UI
│   ├── astro.config.mjs
│   └── src/
├── scripts/
│   └── backup-to-csv.ps1
├── CLAUDE.md              # this file
└── README.md
```

---

## 9. Setup instructions

### Common prerequisites (both options)
1. Go (for Wails) and Node.js (for Astro + frontend tooling)
2. Wails CLI: `go install github.com/wailsapp/wails/v2/cmd/wails@latest`
3. `wails doctor` should report no missing dependencies

### If Option A (Supabase/Docker)
1. Enable WSL2: `wsl --install` (admin PowerShell, reboot)
2. Install Docker Desktop, set to use the WSL2 backend
3. Install Supabase CLI: `npm install -g supabase`
4. `supabase init` in the repo root
5. Drop `db/migrations/001_init_schema.sql` into `supabase/migrations/`
6. `supabase start` — note the printed API URL, DB URL, Studio URL, anon key
7. Confirm tables exist via Studio (`localhost:54323`)

### If Option B (native Postgres, no Docker)
1. Install PostgreSQL for Windows (standard installer, no admin friction)
2. Create the database: `createdb trove`
3. Apply schema: `psql trove -f db/migrations/001_init_schema.sql`
4. Download the PostgREST binary for Windows, point it at the `trove` database via a config file
5. Run PostgREST — it now serves a REST API for every table at e.g. `localhost:3000`
6. Confirm with `curl localhost:3000/assets`

### Then, both options
1. In `desktop-app/`, wire the frontend's API client to whichever local URL you're running (Supabase's `localhost:54321` or PostgREST's `localhost:3000`)
2. In `web-app/`, do the same
3. Test the barcode scanner: focus a text field, scan a test barcode, confirm it types the code + Enter

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
$dumpDir = "C:\Users\<user>\Google Drive\TroveBackups\$(Get-Date -Format yyyy-MM-dd)"
New-Item -ItemType Directory -Path $dumpDir -Force
$tables = @("assets","bookings","custody_events","activity_log","locations","categories","kits")
foreach ($t in $tables) {
  psql "postgresql://<connection-string>" -c "\copy $t TO '$dumpDir\$t.csv' WITH CSV HEADER"
}
```
Test a full restore at least once before go-live: wipe a scratch database, reapply the migration, reload from CSV, confirm row counts match.

---

## 12. Build timeline (9 weeks total)

**Weeks 1–2 (complete/in progress)** — Environment prep, tooling installed, initial planning.

**Week 3 — Decide & initialize external software**
Finalize Option A vs. Option B (Section 4). Get Postgres/Supabase and PostgREST/Auth running independently of each other and confirmed healthy before wiring anything together.

**Week 4 — Database connectivity**
Add test items to the database. Verify read/modify/tag operations work directly against the DB. Connect Wails/Go to the database so it can mutate data (create, update, delete).

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

## 13. Open decisions to resolve before/during Week 3

- [ ] Option A vs. Option B backend (Section 4)
- [ ] Svelte vs. React for the frontend components (shared between Wails and Astro)
- [ ] Which cloud sync client is already installed on the closet PC (Drive/OneDrive/Dropbox) — determines the backup folder path
- [ ] Barcode scanner model to purchase (Week 7) — confirm it's a standard HID keyboard-wedge scanner, not one requiring proprietary software
