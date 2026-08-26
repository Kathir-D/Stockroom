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
