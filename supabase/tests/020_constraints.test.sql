-- Behavioural tests for the guarantees the application leans on: uniqueness,
-- NOT NULL, column defaults, and what happens to dependent rows when a parent
-- is deleted. Everything runs inside a transaction that is rolled back.
begin;
create extension if not exists pgtap;
select no_plan();

-- Fixtures -------------------------------------------------------------------
insert into profiles (id, email, full_name) values
  ('11111111-0000-0000-0000-000000000001', 'owner@test.local', 'Fixture Owner'),
  ('11111111-0000-0000-0000-000000000002', 'student@test.local', 'Fixture Student');
insert into categories (id, name) values
  ('11111111-0000-0000-0000-000000000010', 'Fixture Type'),
  ('11111111-0000-0000-0000-000000000011', 'Fixture Model');
update categories set parent_id = '11111111-0000-0000-0000-000000000010'
  where id = '11111111-0000-0000-0000-000000000011';
insert into locations (id, name, type) values
  ('11111111-0000-0000-0000-000000000020', 'Fixture Building', 'building');
insert into assets (id, asset_tag, name, category_id, location_id, serial_number) values
  ('11111111-0000-0000-0000-000000000030', 'FIX-001', 'Fixture Camera',
   '11111111-0000-0000-0000-000000000011', '11111111-0000-0000-0000-000000000020', 'FIXSN-001');
insert into tags (id, name) values ('11111111-0000-0000-0000-000000000040', 'fixture-tag');

-- Uniqueness -----------------------------------------------------------------
select throws_ok(
  $$insert into assets (asset_tag, name) values ('FIX-001', 'Duplicate tag')$$,
  '23505', null, 'assets.asset_tag is unique -- two stickers cannot share a tag');
select throws_ok(
  $$insert into profiles (email) values ('owner@test.local')$$,
  '23505', null, 'profiles.email is unique');
select throws_ok(
  $$insert into categories (name) values ('Fixture Type')$$,
  '23505', null, 'categories.name is unique');
select throws_ok(
  $$insert into tags (name) values ('fixture-tag')$$,
  '23505', null, 'tags.name is unique');

-- v1 will make serial_number unique (CLAUDE.md §6.2). It is deliberately NOT
-- unique yet; this test documents the current state so the migration that
-- changes it is a conscious step.
select lives_ok(
  $$insert into assets (asset_tag, name, serial_number) values ('FIX-DUP', 'Same serial', 'FIXSN-001')$$,
  'assets.serial_number is not unique yet (v1 migration adds the unique index)');

-- NOT NULL -------------------------------------------------------------------
select throws_ok(
  $$insert into assets (asset_tag) values ('FIX-002')$$,
  '23502', null, 'assets.name is required');
select throws_ok(
  $$insert into assets (name) values ('No tag')$$,
  '23502', null, 'assets.asset_tag is required');
select throws_ok(
  $$insert into profiles (full_name) values ('No email')$$,
  '23502', null, 'profiles.email is required by the base schema (v1 relaxes it)');
select throws_ok(
  $$insert into custody_events (asset_id, custodian_id) values ('11111111-0000-0000-0000-000000000030', null)$$,
  '23502', null, 'custody_events.custodian_id is required -- custody always has an owner');

-- Enum inputs ----------------------------------------------------------------
select throws_ok(
  $$insert into assets (asset_tag, name, status) values ('FIX-003', 'Bad status', 'unavailable')$$,
  '22P02', null, '''unavailable'' is rejected until the v1 migration adds it to asset_status');
select throws_ok(
  $$insert into assets (asset_tag, name, status) values ('FIX-004', 'Bad status', 'broken')$$,
  '22P02', null, 'an arbitrary status string is rejected by the enum');

-- Defaults -------------------------------------------------------------------
insert into assets (id, asset_tag, name)
  values ('11111111-0000-0000-0000-000000000031', 'FIX-DEFAULTS', 'Defaults probe');
select is(status::text, 'available', 'a new asset defaults to available')
  from assets where id = '11111111-0000-0000-0000-000000000031';
select is(custom_fields, '{}'::jsonb, 'custom_fields defaults to an empty object')
  from assets where id = '11111111-0000-0000-0000-000000000031';
select ok(created_at is not null and updated_at is not null, 'created_at/updated_at are populated on insert')
  from assets where id = '11111111-0000-0000-0000-000000000031';

insert into profiles (id, email) values ('11111111-0000-0000-0000-000000000003', 'default-role@test.local');
select is(role::text, 'member', 'a new profile defaults to the member role')
  from profiles where id = '11111111-0000-0000-0000-000000000003';
select ok(password_hash is null, 'password_hash starts null (roster imports have no password)')
  from profiles where id = '11111111-0000-0000-0000-000000000003';

insert into locations (id, name) values ('11111111-0000-0000-0000-000000000021', 'Default type');
select is(type::text, 'other', 'a new location defaults to type other')
  from locations where id = '11111111-0000-0000-0000-000000000021';

-- Generated ids are distinct (uuid_generate_v4 default is wired up).
select isnt(
  (select id from assets where asset_tag = 'FIX-DEFAULTS'),
  (select id from assets where asset_tag = 'FIX-DUP'),
  'ids are generated per row');

-- Foreign keys: rejection of dangling references ------------------------------
select throws_ok(
  $$insert into assets (asset_tag, name, category_id) values ('FIX-005', 'Dangling', '99999999-9999-9999-9999-999999999999')$$,
  '23503', null, 'assets.category_id must reference a real category');
select throws_ok(
  $$insert into custody_events (asset_id, custodian_id, checked_out_by)
    values ('99999999-9999-9999-9999-999999999999', '11111111-0000-0000-0000-000000000002', '11111111-0000-0000-0000-000000000002')$$,
  '23503', null, 'custody_events.asset_id must reference a real asset');
select throws_ok(
  $$insert into asset_tags (asset_id, tag_id) values ('11111111-0000-0000-0000-000000000030', '99999999-9999-9999-9999-999999999999')$$,
  '23503', null, 'asset_tags.tag_id must reference a real tag');

-- Foreign key actions declared in the schema ---------------------------------
select fk_ok('public','assets','category_id','public','categories','id', 'assets.category_id references categories');
select fk_ok('public','assets','location_id','public','locations','id', 'assets.location_id references locations');
select fk_ok('public','custody_events','asset_id','public','assets','id', 'custody_events.asset_id references assets');
select fk_ok('public','custody_events','custodian_id','public','profiles','id', 'custody_events.custodian_id references profiles');
select fk_ok('public','asset_tags','asset_id','public','assets','id', 'asset_tags.asset_id references assets');
select fk_ok('public','kit_items','kit_id','public','kits','id', 'kit_items.kit_id references kits');
select fk_ok('public','saved_filters','user_id','public','profiles','id', 'saved_filters.user_id references profiles');

-- Deleting an asset must clean up everything hanging off it -------------------
insert into kits (id, name) values ('11111111-0000-0000-0000-000000000050', 'Fixture Kit');
insert into kit_items (kit_id, asset_id) values ('11111111-0000-0000-0000-000000000050', '11111111-0000-0000-0000-000000000030');
insert into asset_tags (asset_id, tag_id) values ('11111111-0000-0000-0000-000000000030', '11111111-0000-0000-0000-000000000040');
insert into bookings (id, asset_id, reserved_by, start_date, end_date)
  values ('11111111-0000-0000-0000-000000000060', '11111111-0000-0000-0000-000000000030',
          '11111111-0000-0000-0000-000000000002', now(), now() + interval '1 day');
insert into custody_events (id, asset_id, custodian_id, checked_out_by)
  values ('11111111-0000-0000-0000-000000000070', '11111111-0000-0000-0000-000000000030',
          '11111111-0000-0000-0000-000000000002', '11111111-0000-0000-0000-000000000002');
insert into activity_log (asset_id, action) values ('11111111-0000-0000-0000-000000000030', 'fixture');

delete from assets where id = '11111111-0000-0000-0000-000000000030';

select is((select count(*) from asset_tags where asset_id = '11111111-0000-0000-0000-000000000030'), 0::bigint,
  'deleting an asset cascades to asset_tags');
select is((select count(*) from kit_items where asset_id = '11111111-0000-0000-0000-000000000030'), 0::bigint,
  'deleting an asset cascades to kit_items');
select is((select count(*) from bookings where asset_id = '11111111-0000-0000-0000-000000000030'), 0::bigint,
  'deleting an asset cascades to bookings');
select is((select count(*) from custody_events where asset_id = '11111111-0000-0000-0000-000000000030'), 0::bigint,
  'deleting an asset cascades to custody_events (history goes with the asset)');
select is((select count(*) from activity_log where asset_id = '11111111-0000-0000-0000-000000000030'), 0::bigint,
  'deleting an asset cascades to activity_log');

-- Deleting a tag or a kit removes only the join rows --------------------------
insert into assets (id, asset_tag, name) values ('11111111-0000-0000-0000-000000000032', 'FIX-006', 'Tagged asset');
insert into asset_tags (asset_id, tag_id) values ('11111111-0000-0000-0000-000000000032', '11111111-0000-0000-0000-000000000040');
delete from tags where id = '11111111-0000-0000-0000-000000000040';
select is((select count(*) from asset_tags where asset_id = '11111111-0000-0000-0000-000000000032'), 0::bigint,
  'deleting a tag cascades to asset_tags');
select is((select count(*) from assets where id = '11111111-0000-0000-0000-000000000032'), 1::bigint,
  'deleting a tag leaves the asset itself alone');

insert into kit_items (kit_id, asset_id) values ('11111111-0000-0000-0000-000000000050', '11111111-0000-0000-0000-000000000032');
delete from kits where id = '11111111-0000-0000-0000-000000000050';
select is((select count(*) from kit_items where asset_id = '11111111-0000-0000-0000-000000000032'), 0::bigint,
  'deleting a kit cascades to kit_items');
select is((select count(*) from assets where id = '11111111-0000-0000-0000-000000000032'), 1::bigint,
  'deleting a kit leaves its assets alone');

-- Deleting a category or location must not delete equipment -------------------
insert into assets (id, asset_tag, name, category_id, location_id) values
  ('11111111-0000-0000-0000-000000000033', 'FIX-007', 'Orphan probe',
   '11111111-0000-0000-0000-000000000011', '11111111-0000-0000-0000-000000000020');

delete from categories where id = '11111111-0000-0000-0000-000000000011';
select is((select count(*) from assets where id = '11111111-0000-0000-0000-000000000033'), 1::bigint,
  'deleting a category keeps the asset');
select ok((select category_id is null from assets where id = '11111111-0000-0000-0000-000000000033'),
  'deleting a category nulls the asset''s category_id');

delete from locations where id = '11111111-0000-0000-0000-000000000020';
select ok((select location_id is null from assets where id = '11111111-0000-0000-0000-000000000033'),
  'deleting a location nulls the asset''s location_id');

-- Category tree: deleting a parent orphans the child rather than removing it.
insert into categories (id, name, parent_id) values
  ('11111111-0000-0000-0000-000000000012', 'Parent node', null),
  ('11111111-0000-0000-0000-000000000013', 'Child node', '11111111-0000-0000-0000-000000000012');
delete from categories where id = '11111111-0000-0000-0000-000000000012';
select ok((select parent_id is null from categories where id = '11111111-0000-0000-0000-000000000013'),
  'deleting a parent category nulls the child''s parent_id instead of cascading');

-- Profiles: custody history blocks deletion ----------------------------------
insert into assets (id, asset_tag, name) values ('11111111-0000-0000-0000-000000000034', 'FIX-008', 'Held asset');
insert into custody_events (asset_id, custodian_id, checked_out_by)
  values ('11111111-0000-0000-0000-000000000034', '11111111-0000-0000-0000-000000000002', '11111111-0000-0000-0000-000000000002');
select throws_ok(
  $$delete from profiles where id = '11111111-0000-0000-0000-000000000002'$$,
  '23503', null, 'a profile with custody history cannot be deleted (history stays intact)');

-- A profile with no dependents deletes, and its saved filters go with it.
insert into saved_filters (user_id, name, filter_json)
  values ('11111111-0000-0000-0000-000000000003', 'Mine', '{"status":"available"}'::jsonb);
delete from profiles where id = '11111111-0000-0000-0000-000000000003';
select is((select count(*) from saved_filters where user_id = '11111111-0000-0000-0000-000000000003'), 0::bigint,
  'deleting a profile cascades to saved_filters');

-- Deleting a booking leaves the custody row but clears the link ---------------
insert into bookings (id, asset_id, reserved_by, start_date, end_date)
  values ('11111111-0000-0000-0000-000000000061', '11111111-0000-0000-0000-000000000034',
          '11111111-0000-0000-0000-000000000002', now(), now() + interval '1 day');
insert into custody_events (id, asset_id, booking_id, custodian_id, checked_out_by)
  values ('11111111-0000-0000-0000-000000000071', '11111111-0000-0000-0000-000000000034',
          '11111111-0000-0000-0000-000000000061', '11111111-0000-0000-0000-000000000002',
          '11111111-0000-0000-0000-000000000002');
delete from bookings where id = '11111111-0000-0000-0000-000000000061';
select ok((select booking_id is null from custody_events where id = '11111111-0000-0000-0000-000000000071'),
  'deleting a booking nulls custody_events.booking_id and keeps the custody record');

select * from finish();
rollback;
