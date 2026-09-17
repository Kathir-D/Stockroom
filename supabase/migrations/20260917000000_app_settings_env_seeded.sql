-- One-time marker for the .env seeding pass (docs/design/backup.md §C.2).
--
-- EnsureSettings copies .env into any settings column that is still null on
-- start-up. Expressed as `coalesce(col, nullif($1, ''))` and nothing else,
-- that is not the first-boot seed it is documented to be -- it is a per-boot
-- one. Clearing a field in the admin panel writes NULL (see `nullable` in
-- settings.go), so the next restart finds the column null again and puts the
-- .env value straight back. The admin's change is undone by a reboot that
-- nobody would connect to it, which is exactly the "bootstrap fallback
-- quietly becoming the source of truth" that §C.2 rules out.
--
-- The fix is to record that the seed has happened rather than infer it from a
-- column being empty, because "empty" is also a legitimate thing for an admin
-- to choose. After the first attempt -- including one that seeded nothing,
-- because .env was blank -- the environment never writes here again.
--
-- Deliberately no backfill to true. This column and the table it is on ship in
-- the same change, so there is no installation that has been seeded but is not
-- marked; a fresh database must land on false or first boot would never seed
-- at all.
alter table app_settings
  add column if not exists env_seeded boolean not null default false;

comment on column app_settings.env_seeded is
  'True once EnsureSettings has run its one .env seeding pass. Keeps a cleared setting cleared across restarts.';
