-- Backup configuration moves out of .env and into the database
-- (docs/design/backup.md §C.2).
--
-- Why: after the one-time software install, no admin or student should ever
-- have to open a text editor to change where backups go, how long they are
-- kept, or which account they are pushed to. A setting that lives in .env is a
-- setting only whoever set the machine up can change, and they are not the
-- person who will be standing in the closet in eighteen months.
--
-- .env keeps one job: seeding these columns the first time the server starts
-- against a fresh database. See EnsureSettings in internal/stockroom/settings.go.
--
-- One row, forever. The `check (id)` trick is the standard way to say that: the
-- primary key can only ever hold `true`, so a second insert collides instead of
-- producing a second configuration nobody can tell apart from the first.

create table if not exists app_settings (
  id                    boolean primary key default true check (id),

  -- Where the dated database folders and the photo mirror are written. Null
  -- means "not configured yet", which the backup endpoints report as a 503
  -- naming the setting rather than failing as a bug.
  backup_dir            text,
  photo_backup_dir      text,

  -- Retention and scheduling. The bounds are repeated in the settings endpoint
  -- so a bad number is a 400 naming the field rather than a 500 carrying a
  -- constraint name, but they live here too because the database is the last
  -- line and a hand-written UPDATE in Studio goes through it.
  --
  -- Zero is valid for schedule_hour (midnight is an hour) and for
  -- photo_min_free_gb (a zero threshold means "never warn me", which is a
  -- coherent request). Zero is refused for the other three, because a zero
  -- *interval* or *count* means "do this every time": keep_days = 0 re-copies
  -- every photo nightly and prunes the backup it just wrote, stale_hours = 0
  -- makes every run stale the instant it finishes, and
  -- photo_max_generations = 0 turns the alert on at the first rollover and
  -- never off. A warning that is always on is a warning nobody reads.
  keep_days             integer not null default 90 check (keep_days >= 1),
  stale_hours           integer not null default 48 check (stale_hours >= 1),
  schedule_hour         integer not null default 2  check (schedule_hour between 0 and 23),

  -- Google Drive, pushed with the rclone binary (docs/design/backup.md §E.3).
  drive_enabled         boolean not null default false,
  drive_remote          text,
  drive_path            text default 'stockroom',

  -- GitHub, pushed with the REST API and no git binary (§E.4).
  github_enabled        boolean not null default false,
  github_repo           text,
  github_token          text,

  -- Null means the archive leaves the machine unencrypted, which is the
  -- default and a deliberate decision (§C.5): losing a passphrase loses the
  -- backup, and for this deployment that availability risk is worse than the
  -- confidentiality risk it would buy.
  archive_passphrase    text,

  -- The photo mirror never deletes anything, so it only grows. These two bound
  -- it with an alert rather than a purge (§E.2).
  photo_min_free_gb     integer not null default 5 check (photo_min_free_gb >= 0),
  photo_max_generations integer not null default 8 check (photo_max_generations >= 1),

  updated_at            timestamptz not null default now()
);

-- The one row. `on conflict do nothing` so re-running the migration against a
-- database that already has it is a no-op rather than an error.
insert into app_settings (id) values (true) on conflict (id) do nothing;

comment on table app_settings is
  'Single-row backup configuration (docs/design/backup.md §C.2). github_token and archive_passphrase are redacted from the CSV export by exportRedactions in internal/stockroom/backup.go; adding a secret column here means adding it there too.';
