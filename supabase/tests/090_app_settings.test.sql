-- Backup configuration lives in the database, not .env
-- (migrations/20260916090000_app_settings.sql, docs/design/backup.md §C.2).
--
-- Two things are pinned here rather than left to the Go layer: that there can
-- only ever be one row, and that the bounded numbers really are bounded. The
-- settings endpoint checks the same values first, so a bad number is a 400
-- naming the field -- but the endpoint is the readable copy, not the only one,
-- and a hand-written UPDATE in Studio goes straight through these.
begin;
create extension if not exists pgtap;
select no_plan();

select has_table('public', 'app_settings', 'table app_settings exists');

select has_column('public', 'app_settings', c, format('app_settings.%I exists', c))
from unnest(array[
  'id','backup_dir','photo_backup_dir','keep_days','stale_hours','schedule_hour',
  'drive_enabled','drive_remote','drive_path',
  'github_enabled','github_repo','github_token','archive_passphrase',
  'photo_min_free_gb','photo_max_generations','photo_wall_size',
  'max_checkout_days','overdue_blocks_checkout','session_idle_minutes','scan_threshold_ms','closed_dates',
  'updated_at'
]) as c;

-- The single row the migration inserts.
select is(
  (select count(*)::int from app_settings),
  1,
  'app_settings holds exactly one row');

-- A second row is impossible: the primary key can only hold true, so a second
-- insert collides rather than producing a configuration nobody can tell apart
-- from the first.
select throws_ok(
  $$insert into app_settings (id) values (true)$$,
  '23505',
  null,
  'a second settings row collides on the primary key');
select throws_ok(
  $$insert into app_settings (id) values (false)$$,
  '23514',
  null,
  'id = false is refused by the check constraint, so there is no second row to have');

-- The bounds. Zero is valid for schedule_hour (midnight is an hour) and for
-- photo_min_free_gb (a zero threshold means "never warn me"). Zero is refused
-- for the other three, because a zero interval or count means "do this every
-- time", which is the always-on failure the bound exists to prevent.
select throws_ok(
  $$update app_settings set keep_days = 0$$, '23514', null,
  'keep_days = 0 is refused: it would re-copy every photo nightly and prune the backup it just wrote');
select throws_ok(
  $$update app_settings set stale_hours = 0$$, '23514', null,
  'stale_hours = 0 is refused: every run would be stale the instant it finished');
select throws_ok(
  $$update app_settings set photo_max_generations = 0$$, '23514', null,
  'photo_max_generations = 0 is refused: the alert would be on from the first rollover and never off');
select throws_ok(
  $$update app_settings set schedule_hour = 24$$, '23514', null,
  'schedule_hour = 24 names a time that does not exist, so backups would silently never fire');
select throws_ok(
  $$update app_settings set photo_min_free_gb = -1$$, '23514', null,
  'a negative disk-headroom threshold has no reading');
select throws_ok(
  $$update app_settings set photo_wall_size = 401$$, '23514', null,
  'photo_wall_size = 401 is refused: the set is held in memory, and 400 is the bound');
select throws_ok(
  $$update app_settings set photo_wall_size = 9$$, '23514', null,
  'photo_wall_size = 9 is refused: two strips need more than that');
select throws_ok(
  $$update app_settings set max_checkout_days = 0$$, '23514', null,
  'max_checkout_days = 0 is refused: nothing could be checked out');
select throws_ok(
  $$update app_settings set max_checkout_days = 61$$, '23514', null,
  'max_checkout_days = 61 is refused');
select throws_ok(
  $$update app_settings set session_idle_minutes = 0$$, '23514', null,
  'session_idle_minutes = 0 is refused: null is how the .env value applies');
select lives_ok(
  $$update app_settings set session_idle_minutes = null$$,
  'session_idle_minutes may be null');
select throws_ok(
  $$update app_settings set scan_threshold_ms = 201$$, '23514', null,
  'scan_threshold_ms = 201 is refused: a typist would read as a scanner');
select throws_ok(
  $$update app_settings set closed_dates = (select array_agg(d::date) from generate_series(date '2026-01-01', date '2027-02-05', interval '1 day') d)$$,
  '23514', null,
  'closed_dates holds at most 400 dates');

select lives_ok(
  $$update app_settings set schedule_hour = 0, photo_min_free_gb = 0$$,
  'midnight and a switched-off free-space warning are both ordinary');

select * from finish();
rollback;
