-- The activity log is append-only and the closet camera's tables exist
-- (migrations/20260926090000_activity_log_and_closet_camera.sql, ADR 0003).
-- The Go suite checks the same refusals through the server's connection; this
-- pins them for a hand-written statement in Studio too.
begin;
create extension if not exists pgtap;
select no_plan();

select has_table('public', 'closet_visits', 'table closet_visits exists');
select has_table('public', 'camera_settings', 'table camera_settings exists');
select has_column('public', 'activity_log', c, format('activity_log.%I exists', c))
from unnest(array['category','summary','actor_name','asset_label','via_scanner','visit_id']) as c;

select is(
  (select count(*)::int from pg_constraint
    where conrelid = 'activity_log'::regclass and contype = 'f'),
  0,
  'activity_log has no foreign keys, so deleting an asset or account keeps its rows');

insert into activity_log (action, category, summary) values ('pgtap_marker', 'admin', 'pgTAP');

select throws_ok(
  $$update activity_log set summary = 'edited' where action = 'pgtap_marker'$$,
  '42501', null, 'an activity row cannot be updated');
select throws_ok(
  $$delete from activity_log where action = 'pgtap_marker'$$,
  '42501', null, 'an activity row cannot be deleted');
select throws_ok(
  $$truncate activity_log$$,
  '42501', null, 'the activity log cannot be truncated');

select throws_ok(
  $$insert into activity_log (action, category) values ('x', 'bogus')$$,
  '23514', null, 'the category is one of the five the timeline filters on');

select is((select count(*)::int from camera_settings), 1, 'camera_settings holds exactly one row');
select col_default_is('public', 'camera_settings', 'enabled', 'false', 'the camera ships off');

select * from finish();
rollback;
