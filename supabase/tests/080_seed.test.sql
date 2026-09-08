-- Sanity check on supabase/seed.sql, which loads on `supabase db reset` and is
-- what developers demo against. It asserts the seeded rows are present and
-- coherent; extra rows added locally are ignored. If this fails after you have
-- been editing data by hand, re-run `supabase db reset`.
begin;
create extension if not exists pgtap;
select no_plan();

-- Category tree: Type -> Category -> Model, from Catagories.md ---------------
select set_eq(
  $$select name from categories where parent_id is null$$,
  array['Cameras/Bodies','Lenses','Lights','Audio Stuff','Physical Bags, etc.','Tripods/Monopods','Batteries','Misc'],
  'the eight top-level types from Catagories.md are seeded');

with recursive tree as (
  select id, name, 1 as depth from categories where parent_id is null
  union all
  select c.id, c.name, tree.depth + 1 from categories c join tree on c.parent_id = tree.id
)
select is(max(depth), 3, 'the seeded tree is exactly three levels deep') from tree;

select set_eq(
  $$select c.name from categories c join categories p on p.id = c.parent_id where p.name = 'Lenses'$$,
  array['Zooms','Primes','Lens Accessories'],
  'Lenses has the Zooms, Primes and Lens Accessories categories');

select is(
  (select count(*) from categories c join categories p on p.id = c.parent_id where p.name = 'Camera Model'),
  13::bigint,
  'all thirteen camera models are seeded under Camera Model');

select is(
  (select count(*) from categories c join categories p on p.id = c.parent_id where p.name = 'Primes'),
  0::bigint,
  'Primes is seeded empty, as Catagories.md says');

-- Every seeded asset hangs off a Model (depth 3) node, never a Type or
-- Category, because that is the only level the browse filters resolve to.
with recursive tree as (
  select id, 1 as depth from categories where parent_id is null
  union all
  select c.id, tree.depth + 1 from categories c join tree on c.parent_id = tree.id
)
select is(
  (select count(*) from assets a join tree on tree.id = a.category_id
    where a.id::text like '00000000-0000-0000-0000-0000000001%' and tree.depth <> 3),
  0::bigint,
  'every seeded asset points at a Model node');

-- Assets --------------------------------------------------------------------
select is(
  (select count(*) from assets where id::text like '00000000-0000-0000-0000-0000000001%'),
  12::bigint,
  'all twelve seeded assets are present');

select ok(
  (select bool_and(serial_number is not null and category_id is not null)
     from assets where id::text like '00000000-0000-0000-0000-0000000001%'),
  'every seeded asset has a serial number and a category');

select ok(
  (select count(*) > 0 from assets where serial_number = 'T7iBat-001'),
  'a model-prefixed linear serial (T7iBat-001) is seeded');

-- The seed covers each v1 status so the UI has something to render for all
-- three, and nothing else.
select set_eq(
  $$select distinct status::text from assets where id::text like '00000000-0000-0000-0000-0000000001%'$$,
  array['available','checked_out','unavailable'],
  'the seed uses exactly the three v1 statuses');

-- Accounts ------------------------------------------------------------------
select is((select student_number from profiles where id = '00000000-0000-0000-0000-000000000020'), '100001',
  'the seeded admin has a student number');
select is((select is_admin from profiles where id = '00000000-0000-0000-0000-000000000020'), true,
  'the seeded admin is an admin');
select ok((select password_hash like '$2a$%' from profiles where id = '00000000-0000-0000-0000-000000000020'),
  'the seeded admin has a bcrypt password hash (typed login works)');

select is((select student_number from profiles where id = '00000000-0000-0000-0000-000000000021'), '200001',
  'the seeded student has a student number');
select is((select is_admin from profiles where id = '00000000-0000-0000-0000-000000000021'), false,
  'the seeded student is not an admin');
select ok((select password_hash is null and email is null from profiles where id = '00000000-0000-0000-0000-000000000021'),
  'the seeded student has no password and no email (the roster-import shape)');

-- Custody -------------------------------------------------------------------
-- Every checked_out asset must have an open custody row, or the status and
-- the custody trail disagree.
select is(
  (select count(*) from assets a
    where a.status = 'checked_out' and a.id::text like '00000000-0000-0000-0000-0000000001%'
      and not exists (select 1 from active_custody ac where ac.asset_id = a.id)),
  0::bigint,
  'every seeded checked_out asset has an open custody row');
select is(
  (select count(*) from active_custody where custodian_id = '00000000-0000-0000-0000-000000000021'),
  2::bigint,
  'the student holds the two checked-out items');
select is(
  (select count(*) from overdue_custody where custodian_id = '00000000-0000-0000-0000-000000000021'),
  0::bigint,
  'nothing seeded is overdue, so the student is not blocked from checking out');

select * from finish();
rollback;
