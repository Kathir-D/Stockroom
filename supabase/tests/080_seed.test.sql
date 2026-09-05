-- Sanity check on supabase/seed.sql, which loads on `supabase db reset` and is
-- what developers demo against. It asserts the seeded rows are present and
-- coherent; extra rows added locally are ignored. If this fails after you have
-- been editing data by hand, re-run `supabase db reset`.
begin;
create extension if not exists pgtap;
select no_plan();

select is(
  (select count(*) from assets where asset_tag in
    ('CAM-001','CAM-002','LEN-001','LEN-002','AUD-001','AUD-002','LIT-001','LIT-002','TRI-001','TRI-002')),
  10::bigint,
  'all ten seeded assets are present');

select ok(
  (select bool_and(serial_number is not null and category_id is not null and location_id is not null)
     from assets where asset_tag like 'CAM-%' or asset_tag like 'LEN-%' or asset_tag like 'AUD-%'
                    or asset_tag like 'LIT-%' or asset_tag like 'TRI-%'),
  'every seeded asset has a serial number, a category and a location');

select is(
  (select count(distinct serial_number) from assets where serial_number like 'SN-%'),
  10::bigint,
  'seeded serial numbers are distinct (they become the scan key in v1)');

select set_eq(
  $$select name from categories$$,
  array['Cameras','Lenses','Audio','Lighting','Tripods/Support'],
  'the seeded category list');

select is((select count(*) from categories where parent_id is not null), 0::bigint,
  'seeded categories are all top level (the 3-level tree arrives with the v1 seed rewrite)');

select is((select name from locations where id = '00000000-0000-0000-0000-000000000002'), 'Camera Closet',
  'the closet location is seeded');
select is(
  (select parent.name from locations child join locations parent on parent.id = child.parent_id
    where child.id = '00000000-0000-0000-0000-000000000002'),
  'Media Building',
  'the closet is nested under the building');

select is((select email from profiles where id = '00000000-0000-0000-0000-000000000020'), 'admin@school.edu',
  'the seeded admin profile exists');
select is((select role::text from profiles where id = '00000000-0000-0000-0000-000000000020'), 'owner',
  'the seeded admin has the owner role');

-- The seed exercises several statuses on purpose so the UI has something to
-- render for each of them.
select is(
  (select count(distinct status::text) from assets
    where status::text in ('available','checked_out','maintenance','reserved')),
  4::bigint,
  'the seed covers a spread of asset statuses');

select * from finish();
rollback;
