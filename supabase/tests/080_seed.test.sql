-- Sanity check on supabase/seed.sql, which loads on `supabase db reset` and is
-- what developers demo against. It asserts the seeded rows are present and
-- coherent; extra rows added locally are ignored. If this fails after you have
-- been editing data by hand, re-run `supabase db reset`.
begin;
create extension if not exists pgtap;
select no_plan();

-- "Seeded" means the fixed id block seed.sql uses for assets. Naming it once
-- keeps the prefix out of every assertion below.
create temporary view seeded_assets as
  select * from assets where id::text like '00000000-0000-0000-0000-0000000001%';

-- The tree, by depth, so the assertions below can ask about a level by name.
create temporary view category_depth as
  with recursive tree as (
    select id, name, parent_id, 1 as depth from categories where parent_id is null
    union all
    select c.id, c.name, c.parent_id, tree.depth + 1 from categories c join tree on c.parent_id = tree.id
  )
  select * from tree;

-- Category tree: Type -> Category -> Model, from Catagories.md ---------------
select set_eq(
  $$select name from categories where parent_id is null$$,
  array['Cameras/Bodies','Lenses','Lights','Audio Stuff','Physical Bags, etc.','Tripods/Monopods','Batteries','Misc'],
  'the eight top-level types from Catagories.md are seeded');

select is(max(depth), 3, 'the seeded tree is exactly three levels deep') from category_depth;

select set_eq(
  $$select c.name from categories c join categories p on p.id = c.parent_id where p.name = 'Lenses'$$,
  array['Zooms','Primes','Accessories'],
  'Lenses keeps the Zooms, Primes and Accessories names Catagories.md gives it');

select is(
  (select count(*) from categories c join categories p on p.id = c.parent_id where p.name = 'Camera Model'),
  13::bigint,
  'all thirteen camera models are seeded under Camera Model');

-- Primes is the one branch that stops short of depth 3, because Catagories.md
-- records no primes in inventory. Pin that down as the only exception, so a
-- Category that loses its Models by accident is caught instead of blending in.
select is(
  (select count(*) from categories c join categories p on p.id = c.parent_id where p.name = 'Primes'),
  0::bigint,
  'Primes is seeded empty, as Catagories.md says');

select set_eq(
  $$select d.name from category_depth d
     where d.depth < 3 and not exists (select 1 from categories c where c.parent_id = d.id)$$,
  array['Primes'],
  'Primes is the only branch that does not reach the Model level');

-- Every seeded asset hangs off a Model (depth 3) node, never a Type or
-- Category, because that is the only level the browse filters resolve to.
select is(
  (select count(*) from seeded_assets a join category_depth d on d.id = a.category_id where d.depth <> 3),
  0::bigint,
  'every seeded asset points at a Model node');

-- Assets --------------------------------------------------------------------
select is((select count(*) from seeded_assets), 16::bigint,
  'all sixteen seeded assets are present');

select ok(
  (select bool_and(serial_number is not null and category_id is not null) from seeded_assets),
  'every seeded asset has a serial number and a category');

select ok(
  (select count(*) > 0 from seeded_assets where serial_number = 'T7IBAT-001'),
  'a model-prefixed linear serial (T7IBAT-001) is seeded');

-- The browse list groups units by name, so a name carrying its own unit number
-- would split one model into N groups of one and the "2 of 3 available" count
-- would never appear. Guard the shape, not one example of it.
select is(
  (select count(*) from seeded_assets where name ~ '#[0-9]+$'), 0::bigint,
  'no seeded asset name ends in a unit number: the serial tells units apart');

select ok(
  (select count(*) >= 3 from seeded_assets where name = 'Canon T7i'),
  'at least three units share the Canon T7i name, so grouping has something to group');

select is(
  (select count(distinct serial_number) from seeded_assets),
  (select count(*) from seeded_assets),
  'every seeded serial number is distinct: it is the scan key');

-- The seed covers each v1 status so the UI has something to render for all
-- three, and nothing else.
select set_eq(
  $$select distinct status::text from seeded_assets$$,
  array['available','checked_out','unavailable'],
  'the seed uses exactly the three v1 statuses');

-- Accounts ------------------------------------------------------------------
select is((select student_number from profiles where id = '00000000-0000-0000-0000-000000000020'), '123456',
  'the seeded admin has a student number');
select is((select is_admin from profiles where id = '00000000-0000-0000-0000-000000000020'), true,
  'the seeded admin is an admin');
select ok((select password_hash like '$2a$%' from profiles where id = '00000000-0000-0000-0000-000000000020'),
  'the seeded admin has a bcrypt password hash (typed login works)');

select is((select student_number from profiles where id = '00000000-0000-0000-0000-000000000021'), '234567',
  'the seeded student has a student number');
select is((select is_admin from profiles where id = '00000000-0000-0000-0000-000000000021'), false,
  'the seeded student is not an admin');
select ok((select password_hash like '$2a$%' from profiles where id = '00000000-0000-0000-0000-000000000021'),
  'the seeded student has a bcrypt password hash (typed login works)');

-- Custody -------------------------------------------------------------------
-- Every checked_out asset must have an open custody row, or the status and
-- the custody trail disagree.
select is(
  (select count(*) from seeded_assets a
    where a.status = 'checked_out'
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
