-- The bookings table carries the only non-trivial constraints in the schema:
-- two CHECKs and a GiST exclusion constraint that prevents double-booking.
-- Reservations are out of scope for v1 (CLAUDE.md §2), but the constraints are
-- live and would silently rot without cover.
begin;
create extension if not exists pgtap;
select no_plan();

insert into profiles (id, email) values ('22222222-0000-0000-0000-000000000001', 'booker@test.local');
insert into assets (id, asset_tag, name) values
  ('22222222-0000-0000-0000-000000000010', 'BK-001', 'Booked camera'),
  ('22222222-0000-0000-0000-000000000011', 'BK-002', 'Other camera');
insert into kits (id, name) values ('22222222-0000-0000-0000-000000000020', 'Booked kit');

-- Exactly one of asset_id / kit_id ---------------------------------------------
select throws_ok(
  $$insert into bookings (reserved_by, start_date, end_date)
    values ('22222222-0000-0000-0000-000000000001', '2030-01-01', '2030-01-02')$$,
  '23514', null, 'a booking with neither an asset nor a kit is rejected');
select throws_ok(
  $$insert into bookings (asset_id, kit_id, reserved_by, start_date, end_date)
    values ('22222222-0000-0000-0000-000000000010', '22222222-0000-0000-0000-000000000020',
            '22222222-0000-0000-0000-000000000001', '2030-01-01', '2030-01-02')$$,
  '23514', null, 'a booking cannot name both an asset and a kit');
select lives_ok(
  $$insert into bookings (kit_id, reserved_by, start_date, end_date)
    values ('22222222-0000-0000-0000-000000000020', '22222222-0000-0000-0000-000000000001',
            '2030-01-01', '2030-01-02')$$,
  'a kit-only booking is allowed');

-- Date sanity ------------------------------------------------------------------
select throws_ok(
  $$insert into bookings (asset_id, reserved_by, start_date, end_date)
    values ('22222222-0000-0000-0000-000000000010', '22222222-0000-0000-0000-000000000001',
            '2030-01-02', '2030-01-01')$$,
  '23514', null, 'end_date before start_date is rejected');
select throws_ok(
  $$insert into bookings (asset_id, reserved_by, start_date, end_date)
    values ('22222222-0000-0000-0000-000000000010', '22222222-0000-0000-0000-000000000001',
            '2030-01-01', '2030-01-01')$$,
  '23514', null, 'a zero-length booking is rejected (end must be strictly after start)');
select throws_ok(
  $$insert into bookings (asset_id, reserved_by, start_date)
    values ('22222222-0000-0000-0000-000000000010', '22222222-0000-0000-0000-000000000001', '2030-01-01')$$,
  '23502', null, 'end_date is required');
select throws_ok(
  $$insert into bookings (asset_id, start_date, end_date)
    values ('22222222-0000-0000-0000-000000000010', '2030-01-01', '2030-01-02')$$,
  '23502', null, 'reserved_by is required');

-- Default status ----------------------------------------------------------------
insert into bookings (id, asset_id, reserved_by, start_date, end_date)
  values ('22222222-0000-0000-0000-000000000030', '22222222-0000-0000-0000-000000000010',
          '22222222-0000-0000-0000-000000000001', '2030-03-01', '2030-03-05');
select is(status::text, 'reserved', 'a new booking defaults to reserved')
  from bookings where id = '22222222-0000-0000-0000-000000000030';

-- Double-booking prevention (exclusion constraint) -------------------------------
select throws_ok(
  $$insert into bookings (asset_id, reserved_by, start_date, end_date)
    values ('22222222-0000-0000-0000-000000000010', '22222222-0000-0000-0000-000000000001',
            '2030-03-03', '2030-03-07')$$,
  '23P01', null, 'an overlapping reservation for the same asset is refused');
select throws_ok(
  $$insert into bookings (asset_id, reserved_by, start_date, end_date, status)
    values ('22222222-0000-0000-0000-000000000010', '22222222-0000-0000-0000-000000000001',
            '2030-03-02', '2030-03-03', 'active')$$,
  '23P01', null, 'an active booking cannot overlap a reserved one');
select throws_ok(
  $$insert into bookings (asset_id, reserved_by, start_date, end_date)
    values ('22222222-0000-0000-0000-000000000010', '22222222-0000-0000-0000-000000000001',
            '2030-02-01', '2030-04-01')$$,
  '23P01', null, 'a booking that fully contains an existing one is refused');

-- The range is half-open, so a booking may start exactly when another ends.
select lives_ok(
  $$insert into bookings (asset_id, reserved_by, start_date, end_date)
    values ('22222222-0000-0000-0000-000000000010', '22222222-0000-0000-0000-000000000001',
            '2030-03-05', '2030-03-08')$$,
  'back-to-back bookings are allowed (tstzrange is half-open)');
select lives_ok(
  $$insert into bookings (asset_id, reserved_by, start_date, end_date)
    values ('22222222-0000-0000-0000-000000000010', '22222222-0000-0000-0000-000000000001',
            '2030-02-01', '2030-03-01')$$,
  'a booking ending exactly when another starts is allowed');

-- The constraint is scoped: other assets, and finished/cancelled bookings, are
-- free to overlap.
select lives_ok(
  $$insert into bookings (asset_id, reserved_by, start_date, end_date)
    values ('22222222-0000-0000-0000-000000000011', '22222222-0000-0000-0000-000000000001',
            '2030-03-01', '2030-03-05')$$,
  'a different asset may be booked over the same dates');
select lives_ok(
  $$insert into bookings (asset_id, reserved_by, start_date, end_date, status)
    values ('22222222-0000-0000-0000-000000000010', '22222222-0000-0000-0000-000000000001',
            '2030-03-01', '2030-03-05', 'cancelled')$$,
  'a cancelled booking may overlap a live one');
select lives_ok(
  $$insert into bookings (asset_id, reserved_by, start_date, end_date, status)
    values ('22222222-0000-0000-0000-000000000010', '22222222-0000-0000-0000-000000000001',
            '2030-03-01', '2030-03-05', 'returned')$$,
  'a returned booking may overlap a live one');
select lives_ok(
  $$insert into bookings (kit_id, reserved_by, start_date, end_date)
    values ('22222222-0000-0000-0000-000000000020', '22222222-0000-0000-0000-000000000001',
            '2030-01-01', '2030-01-02')$$,
  'kit bookings are not covered by the exclusion constraint (asset_id is null)');

-- Cancelling a live booking frees the slot.
update bookings set status = 'cancelled' where id = '22222222-0000-0000-0000-000000000030';
select lives_ok(
  $$insert into bookings (asset_id, reserved_by, start_date, end_date)
    values ('22222222-0000-0000-0000-000000000010', '22222222-0000-0000-0000-000000000001',
            '2030-03-03', '2030-03-04')$$,
  'cancelling a booking releases its dates');

select * from finish();
rollback;
