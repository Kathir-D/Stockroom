-- active_custody / overdue_custody are how the app answers "who has what" and
-- "what is late" (CLAUDE.md §7). Their filters are the whole logic, so they get
-- direct coverage.
begin;
create extension if not exists pgtap;
select no_plan();

insert into profiles (id, email, full_name) values
  ('44444444-0000-0000-0000-000000000001', 'custodian@test.local', 'Custodian'),
  ('44444444-0000-0000-0000-000000000002', 'desk@test.local', 'Front Desk');
insert into assets (id, asset_tag, name, status) values
  ('44444444-0000-0000-0000-000000000010', 'VW-OPEN',     'Open item',        'checked_out'),
  ('44444444-0000-0000-0000-000000000011', 'VW-RETURNED', 'Returned item',    'available'),
  ('44444444-0000-0000-0000-000000000012', 'VW-LATE',     'Late item',        'checked_out'),
  ('44444444-0000-0000-0000-000000000013', 'VW-NODUE',    'No due date item', 'checked_out'),
  ('44444444-0000-0000-0000-000000000014', 'VW-LATEBACK', 'Late but back',    'available');

insert into custody_events (id, asset_id, custodian_id, checked_out_by, due_at, checked_in_at, checked_in_by) values
  -- open, due in the future
  ('44444444-0000-0000-0000-000000000020', '44444444-0000-0000-0000-000000000010',
   '44444444-0000-0000-0000-000000000001', '44444444-0000-0000-0000-000000000002',
   now() + interval '3 days', null, null),
  -- closed
  ('44444444-0000-0000-0000-000000000021', '44444444-0000-0000-0000-000000000011',
   '44444444-0000-0000-0000-000000000001', '44444444-0000-0000-0000-000000000002',
   now() + interval '3 days', now(), '44444444-0000-0000-0000-000000000002'),
  -- open and past due
  ('44444444-0000-0000-0000-000000000022', '44444444-0000-0000-0000-000000000012',
   '44444444-0000-0000-0000-000000000001', '44444444-0000-0000-0000-000000000002',
   now() - interval '2 days', null, null),
  -- open with no due date at all
  ('44444444-0000-0000-0000-000000000023', '44444444-0000-0000-0000-000000000013',
   '44444444-0000-0000-0000-000000000001', '44444444-0000-0000-0000-000000000002',
   null, null, null),
  -- returned late: was overdue, but is back
  ('44444444-0000-0000-0000-000000000024', '44444444-0000-0000-0000-000000000014',
   '44444444-0000-0000-0000-000000000001', '44444444-0000-0000-0000-000000000002',
   now() - interval '5 days', now(), '44444444-0000-0000-0000-000000000002');

-- active_custody ---------------------------------------------------------------
select set_eq(
  $$select asset_tag from active_custody where custodian_id = '44444444-0000-0000-0000-000000000001'$$,
  array['VW-OPEN','VW-LATE','VW-NODUE'],
  'active_custody lists exactly the events with no checked_in_at');

select is(asset_name, 'Open item', 'active_custody joins the asset name')
  from active_custody where id = '44444444-0000-0000-0000-000000000020';
select is(asset_tag, 'VW-OPEN', 'active_custody joins the asset tag')
  from active_custody where id = '44444444-0000-0000-0000-000000000020';

-- The view is `ce.*`, so every custody column must come through for the Go
-- ActiveCustody struct to scan.
select has_column('public', 'active_custody', c, format('active_custody exposes %I', c))
from unnest(array[
  'id','asset_id','booking_id','custodian_id','checked_out_by','checked_out_at',
  'due_at','checked_in_at','checked_in_by','condition_out','condition_in','notes',
  'asset_name','asset_tag'
]) as c;

-- overdue_custody ---------------------------------------------------------------
select set_eq(
  $$select asset_tag from overdue_custody where custodian_id = '44444444-0000-0000-0000-000000000001'$$,
  array['VW-LATE'],
  'overdue_custody lists only open events whose due date has passed');

select is((select count(*) from overdue_custody where id = '44444444-0000-0000-0000-000000000023'), 0::bigint,
  'an open loan with no due date is never overdue');
select is((select count(*) from overdue_custody where id = '44444444-0000-0000-0000-000000000024'), 0::bigint,
  'an item returned after its due date is no longer overdue');
select is((select count(*) from overdue_custody where id = '44444444-0000-0000-0000-000000000020'), 0::bigint,
  'an open loan due in the future is not overdue');

-- overdue is a strict subset of active: checking an item in clears both.
update custody_events set checked_in_at = now(), checked_in_by = '44444444-0000-0000-0000-000000000002'
  where id = '44444444-0000-0000-0000-000000000022';
select is((select count(*) from overdue_custody where id = '44444444-0000-0000-0000-000000000022'), 0::bigint,
  'checking in an overdue item removes it from overdue_custody');
select is((select count(*) from active_custody where id = '44444444-0000-0000-0000-000000000022'), 0::bigint,
  'checking in an overdue item removes it from active_custody');

select * from finish();
rollback;
