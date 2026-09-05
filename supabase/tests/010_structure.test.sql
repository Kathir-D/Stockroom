-- Structural contract of the base schema
-- (migrations/20260826173006_init_schema.sql). These assertions are what the
-- Go row structs in internal/stockroom/types.go are written against: if a
-- column is renamed or retyped here, the scans break at runtime, so the shape
-- is pinned down explicitly.
begin;
create extension if not exists pgtap;
select no_plan();

-- Extensions the schema depends on -------------------------------------------
select has_extension('uuid-ossp', 'uuid-ossp is installed (uuid_generate_v4 defaults)');
select has_extension('btree_gist', 'btree_gist is installed (the bookings exclusion constraint needs it)');

-- Tables and views -----------------------------------------------------------
select has_table('public', t, format('table %I exists', t))
from unnest(array[
  'profiles','locations','categories','tags','assets','asset_tags',
  'kits','kit_items','bookings','custody_events','activity_log','saved_filters'
]) as t;

select has_view('public', v, format('view %I exists', v))
from unnest(array['active_custody','overdue_custody']) as v;

-- Enums ----------------------------------------------------------------------
select enum_has_labels('public', 'user_role',
  array['owner','executive_producer','producer','member'],
  'user_role labels');
select enum_has_labels('public', 'asset_status',
  array['available','checked_out','reserved','maintenance','retired','lost'],
  'asset_status labels (v1 adds ''unavailable'' in a later migration)');
select enum_has_labels('public', 'booking_status',
  array['reserved','active','returned','overdue','cancelled'],
  'booking_status labels');
select enum_has_labels('public', 'location_type',
  array['building','floor','room','shelf','other'],
  'location_type labels');

-- Primary keys ---------------------------------------------------------------
select col_is_pk('public', t, 'id', format('%I.id is the primary key', t))
from unnest(array[
  'profiles','locations','categories','tags','assets','kits',
  'bookings','custody_events','activity_log','saved_filters'
]) as t;
select col_is_pk('public', 'asset_tags', array['asset_id','tag_id'], 'asset_tags has a composite primary key');
select col_is_pk('public', 'kit_items', array['kit_id','asset_id'], 'kit_items has a composite primary key');

-- Columns the Go structs scan into ------------------------------------------
select has_column('public', 'assets', c, format('assets.%I exists', c))
from unnest(array[
  'id','asset_tag','name','description','category_id','location_id','status',
  'condition','serial_number','purchase_date','purchase_price',
  'warranty_expiration','custom_fields','created_by','created_at','updated_at'
]) as c;

select has_column('public', 'custody_events', c, format('custody_events.%I exists', c))
from unnest(array[
  'id','asset_id','booking_id','custodian_id','checked_out_by','checked_out_at',
  'due_at','checked_in_at','checked_in_by','condition_out','condition_in','notes'
]) as c;

select has_column('public', 'profiles', c, format('profiles.%I exists', c))
from unnest(array['id','email','password_hash','full_name','role','created_at']) as c;

-- Types that would silently mis-scan if they changed --------------------------
select col_type_is('public', 'assets', 'status', 'asset_status', 'assets.status is the enum, not text');
select col_type_is('public', 'assets', 'custom_fields', 'jsonb', 'assets.custom_fields is jsonb');
select col_type_is('public', 'assets', 'purchase_price', 'numeric(10,2)', 'assets.purchase_price is numeric(10,2)');
select col_type_is('public', 'assets', 'purchase_date', 'date', 'assets.purchase_date is a date, not a timestamp');
select col_type_is('public', 'custody_events', 'checked_out_at', 'timestamp with time zone', 'custody timestamps are timestamptz');
select col_type_is('public', 'custody_events', 'due_at', 'timestamp with time zone', 'due_at is timestamptz');
select col_type_is('public', 'activity_log', 'details', 'jsonb', 'activity_log.details is jsonb');
select col_type_is('public', 'profiles', 'role', 'user_role', 'profiles.role is the enum');

-- Nullability ----------------------------------------------------------------
select col_not_null('public', 'assets', c, format('assets.%I is NOT NULL', c))
from unnest(array['asset_tag','name','status','custom_fields','created_at','updated_at']) as c;
select col_is_null('public', 'assets', c, format('assets.%I is nullable', c))
from unnest(array['description','category_id','location_id','serial_number','condition']) as c;

select col_not_null('public', 'custody_events', c, format('custody_events.%I is NOT NULL', c))
from unnest(array['asset_id','custodian_id','checked_out_by','checked_out_at']) as c;
select col_is_null('public', 'custody_events', 'checked_in_at', 'custody_events.checked_in_at is nullable (open custody)');

-- Indexes that queries in later phases depend on ------------------------------
select has_index('public', 'assets', i, format('index %I exists', i))
from unnest(array['idx_assets_status','idx_assets_category','idx_assets_location','idx_assets_custom_fields','idx_assets_search']) as i;
select has_index('public', 'custody_events', i, format('index %I exists', i))
from unnest(array['idx_custody_asset','idx_custody_open']) as i;
select has_index('public', 'activity_log', 'idx_activity_asset', 'index idx_activity_asset exists');
select has_index('public', 'bookings', i, format('index %I exists', i))
from unnest(array['idx_bookings_asset','idx_bookings_kit','idx_bookings_dates']) as i;

-- idx_custody_open is partial on purpose: it serves "what is out right now".
select matches(
  (select pg_get_expr(indpred, indrelid) from pg_index where indexrelid = 'public.idx_custody_open'::regclass),
  'checked_in_at IS NULL',
  'idx_custody_open is partial on checked_in_at is null');

-- Functions and triggers ------------------------------------------------------
select has_function('public', 'set_updated_at', 'set_updated_at() exists');
select has_function('public', 'log_asset_status_change', 'log_asset_status_change() exists');
select has_trigger('public', 'assets', 'trg_assets_updated_at', 'assets has the updated_at trigger');
select has_trigger('public', 'assets', 'trg_asset_status_log', 'assets has the status-change logging trigger');

select * from finish();
rollback;
