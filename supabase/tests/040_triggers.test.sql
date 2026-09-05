-- The two triggers on assets are the schema's only application logic:
-- trg_assets_updated_at keeps updated_at honest, and trg_asset_status_log
-- writes the audit trail that the custody history is read from.
--
-- Note: now() is the transaction timestamp, so timestamps are compared against
-- a deliberately stale seeded value rather than against each other.
begin;
create extension if not exists pgtap;
select no_plan();

insert into profiles (id, email) values ('33333333-0000-0000-0000-000000000001', 'trigger@test.local');
insert into assets (id, asset_tag, name, description, updated_at, created_at)
  values ('33333333-0000-0000-0000-000000000010', 'TR-001', 'Trigger camera', 'before',
          '2000-01-01T00:00:00Z', '2000-01-01T00:00:00Z');

-- updated_at ------------------------------------------------------------------
update assets set description = 'after' where id = '33333333-0000-0000-0000-000000000010';
select is(updated_at, now(), 'updating a row stamps updated_at with the current time')
  from assets where id = '33333333-0000-0000-0000-000000000010';
select is(created_at, '2000-01-01T00:00:00Z'::timestamptz, 'updating a row leaves created_at alone')
  from assets where id = '33333333-0000-0000-0000-000000000010';

-- A client cannot backdate updated_at; the trigger always wins.
update assets set updated_at = '2001-01-01T00:00:00Z' where id = '33333333-0000-0000-0000-000000000010';
select is(updated_at, now(), 'an explicit updated_at in an UPDATE is overridden by the trigger')
  from assets where id = '33333333-0000-0000-0000-000000000010';

-- ... but an explicit value on INSERT is kept (the trigger is UPDATE-only).
insert into assets (asset_tag, name, updated_at)
  values ('TR-002', 'Insert probe', '2000-06-01T00:00:00Z');
select is(updated_at, '2000-06-01T00:00:00Z'::timestamptz, 'the trigger does not fire on INSERT')
  from assets where asset_tag = 'TR-002';

-- Status-change logging --------------------------------------------------------
select is((select count(*) from activity_log where asset_id = '33333333-0000-0000-0000-000000000010'), 0::bigint,
  'creating an asset and editing its description writes no activity_log rows');

update assets set status = 'checked_out' where id = '33333333-0000-0000-0000-000000000010';
select is((select count(*) from activity_log where asset_id = '33333333-0000-0000-0000-000000000010'), 1::bigint,
  'a status change writes exactly one activity_log row');
select is(action, 'status_change', 'the logged action is status_change')
  from activity_log where asset_id = '33333333-0000-0000-0000-000000000010';
select is(details, '{"to": "checked_out", "from": "available"}'::jsonb, 'the log records the old and new status')
  from activity_log where asset_id = '33333333-0000-0000-0000-000000000010';
select ok(actor_id is null, 'the trigger cannot know the actor, so actor_id is null (the app fills this in)')
  from activity_log where asset_id = '33333333-0000-0000-0000-000000000010';
select ok(created_at is not null, 'the log row is timestamped')
  from activity_log where asset_id = '33333333-0000-0000-0000-000000000010';

-- Re-setting the same status is not a change.
update assets set status = 'checked_out' where id = '33333333-0000-0000-0000-000000000010';
select is((select count(*) from activity_log where asset_id = '33333333-0000-0000-0000-000000000010'), 1::bigint,
  'writing the same status again logs nothing (is distinct from)');

-- A non-status update is not a change either.
update assets set name = 'Renamed' where id = '33333333-0000-0000-0000-000000000010';
select is((select count(*) from activity_log where asset_id = '33333333-0000-0000-0000-000000000010'), 1::bigint,
  'editing other columns logs nothing');

-- Each subsequent transition adds a row: the check-out/check-in trail.
update assets set status = 'available' where id = '33333333-0000-0000-0000-000000000010';
select is((select count(*) from activity_log where asset_id = '33333333-0000-0000-0000-000000000010'), 2::bigint,
  'a second status change appends another row');
select is(
  (select details from activity_log
   where asset_id = '33333333-0000-0000-0000-000000000010'
   order by created_at desc, details->>'to' limit 1),
  '{"to": "available", "from": "checked_out"}'::jsonb,
  'the return transition is logged as checked_out -> available');

-- A bulk status update logs one row per asset, which is what the cart checkout
-- in Phase 4 will do.
insert into assets (id, asset_tag, name) values
  ('33333333-0000-0000-0000-000000000020', 'TR-010', 'Cart item A'),
  ('33333333-0000-0000-0000-000000000021', 'TR-011', 'Cart item B');
update assets set status = 'checked_out'
  where id in ('33333333-0000-0000-0000-000000000020', '33333333-0000-0000-0000-000000000021');
select is(
  (select count(*) from activity_log
   where asset_id in ('33333333-0000-0000-0000-000000000020','33333333-0000-0000-0000-000000000021')),
  2::bigint,
  'a multi-row status update logs one row per asset');

select * from finish();
rollback;
