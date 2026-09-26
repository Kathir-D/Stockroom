-- The complete activity log and the closet camera (ROADMAP §2).
--
-- activity_log was written into by one trigger (asset status changes) and two
-- admin actions. It becomes the one append-only timeline of everything that
-- happens at the closet: walk-ins and walk-outs from the camera, sign-ins,
-- every barcode scan, every checkout and check-in, and every admin change.
-- An admin traces a missing item by reading it (Admin -> Activity).

-- 1. The log outlives what it talks about.
--
-- Both foreign keys go. asset_id cascaded, so deleting an asset deleted its
-- trail -- the one moment somebody might want to read it. actor_id had no
-- action at all, so an account that had ever been logged could not be
-- deleted. The ids stay as plain uuids, and the names they pointed at are
-- snapshotted into actor_name and asset_label at the moment of writing, so a
-- row still reads correctly after the account or the item is gone.
alter table activity_log drop constraint if exists activity_log_asset_id_fkey;
alter table activity_log drop constraint if exists activity_log_actor_id_fkey;

alter table activity_log
  -- closet | account | scan | equipment | admin: the timeline's type filter.
  add column category text not null default 'admin'
    check (category in ('closet', 'account', 'scan', 'equipment', 'admin')),
  -- One readable sentence, written by the server when the row is written, so
  -- the CSV export and a restored backup read without the application.
  add column summary text not null default '',
  add column actor_name text,
  add column asset_label text,
  -- True for every row that a barcode scan caused, whatever it then did: the
  -- timeline's "Scans" filter is this column, not a list of actions.
  add column via_scanner boolean not null default false,
  -- A walked-in / walked-out row points at its visit (and so its recording).
  add column visit_id uuid;

update activity_log set category = 'equipment' where action = 'status_change';

create index idx_activity_time on activity_log (created_at desc);
create index idx_activity_category_time on activity_log (category, created_at desc);
create index idx_activity_actor_time on activity_log (actor_id, created_at desc);

-- 2. Append-only.
--
-- No UPDATE, no DELETE, no TRUNCATE, for anybody, the superuser the server
-- connects as included: a trigger fires for every role. The one exception is
-- the restore, which truncates and reloads every table under
-- `session_replication_role = replica` -- and ordinary triggers do not fire
-- in replica mode, which is exactly the exemption it needs. The restore then
-- puts back every row written after the backup was taken (restore.go), so a
-- restore cannot be used to erase the log either.
create or replace function activity_log_is_append_only() returns trigger as $$
begin
  raise exception 'activity_log is append-only: rows cannot be changed or deleted'
    using errcode = 'insufficient_privilege';
end;
$$ language plpgsql;

create trigger trg_activity_log_no_update
before update or delete on activity_log
for each row execute function activity_log_is_append_only();

create trigger trg_activity_log_no_truncate
before truncate on activity_log
for each statement execute function activity_log_is_append_only();

-- 3. The status trigger stays, as the catch-all for a status change nobody
-- logged: a hand-written UPDATE in Studio, say. When the server makes the
-- change it logs the action itself, in the same transaction, and sets
-- `stockroom.logged` so this trigger does not add a second row for the same
-- event. It also picks up the acting account the server declared.
create or replace function log_asset_status_change() returns trigger as $$
declare
  actor uuid := nullif(current_setting('stockroom.actor_id', true), '')::uuid;
begin
  if TG_OP = 'UPDATE' and old.status is distinct from new.status
     and coalesce(current_setting('stockroom.logged', true), '') <> 'on' then
    insert into activity_log (asset_id, actor_id, action, details, category, summary, actor_name, asset_label)
    values (
      new.id, actor, 'status_change',
      jsonb_build_object('from', old.status, 'to', new.status),
      'equipment',
      format('%s changed from %s to %s', coalesce(new.serial_number, new.name), old.status, new.status),
      (select coalesce(nullif(full_name, ''), trim(coalesce(first_name, '') || ' ' || coalesce(last_name, '')))
         from profiles where id = actor),
      coalesce(new.serial_number, new.name));
  end if;
  return new;
end;
$$ language plpgsql;

-- 4. Closet visits: one row per person the detector tracked, from walking in
-- to walking out. Unlike the log this is mutable -- it gains an end time, a
-- recording, a keep flag, and loses its recording to retention -- and every
-- one of those moments that matters is ALSO a log row.
--
-- The recordings themselves are files in the folder camera_settings names,
-- never in the database, never in a backup and never pushed off the machine.
-- This table is in the backup (it is small and it is what the log links to);
-- the paths it holds simply point at nothing on a machine the files were not
-- copied to.
create table closet_visits (
  id uuid primary key default uuid_generate_v4(),
  detector text not null default 'frigate',
  detector_event_id text not null unique,
  camera text not null,
  started_at timestamptz not null,
  ended_at timestamptz,
  top_score real,
  -- Absolute paths, so changing the recordings folder later does not orphan
  -- the files already recorded.
  snapshot_path text,
  clip_path text,
  clip_bytes bigint,
  keep boolean not null default false,
  kept_by uuid,
  kept_at timestamptz,
  recording_deleted_at timestamptz,
  created_at timestamptz not null default now()
);

create index idx_closet_visits_started on closet_visits (started_at desc);

-- 5. The camera's settings, one row, edited in Admin -> Settings. Their own
-- table rather than more columns on app_settings: nothing here is a secret or
-- a backup setting, and the camera is a separate subsystem that may simply
-- never be turned on.
create table camera_settings (
  id boolean primary key default true check (id),
  enabled boolean not null default false,
  detector_url text not null default 'http://127.0.0.1:5055',
  camera_name text not null default 'closet',
  recordings_dir text,
  retention_days integer not null default 30 check (retention_days between 1 and 3650),
  min_free_gb integer not null default 5 check (min_free_gb between 0 and 100000),
  -- A person tracked for less than this is dropped as a false detection.
  min_visit_seconds integer not null default 2 check (min_visit_seconds between 0 and 600),
  updated_at timestamptz not null default now()
);

insert into camera_settings (id) values (true) on conflict (id) do nothing;
