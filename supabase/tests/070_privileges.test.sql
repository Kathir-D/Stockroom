-- The grant migration (20260826180000_grant_service_role.sql) is a security
-- boundary: the PostgREST roles anon/authenticated must not be able to read the
-- inventory, while service_role keeps full access. Nothing in the current app
-- should depend on this (the Go server connects as postgres, CLAUDE.md §4), but
-- the Supabase REST API is still listening on 54321, so the revoke matters.
begin;
create extension if not exists pgtap;
select no_plan();

-- service_role keeps full access to every table ------------------------------
select table_privs_are('public', t, 'service_role',
  array['SELECT','INSERT','UPDATE','DELETE','TRUNCATE','REFERENCES','TRIGGER'],
  format('service_role has full privileges on %I', t))
from unnest(array[
  'profiles','locations','categories','tags','assets','asset_tags',
  'kits','kit_items','bookings','custody_events','activity_log','saved_filters'
]) as t;

select ok(has_table_privilege('service_role', format('public.%I', v)::regclass, 'select'),
  format('service_role can read the %I view', v))
from unnest(array['active_custody','overdue_custody']) as v;

-- The unauthenticated REST roles must see nothing ----------------------------
select table_privs_are('public', t, r, array[]::text[],
  format('%s has no privileges on %I', r, t))
from unnest(array[
  'profiles','locations','categories','tags','assets','asset_tags',
  'kits','kit_items','bookings','custody_events','activity_log','saved_filters'
]) as t,
unnest(array['anon','authenticated']) as r;

select ok(not has_table_privilege('anon', 'public.profiles'::regclass, 'select'),
  'anon cannot read profiles (password hashes live there)');
select ok(not has_table_privilege('anon', 'public.assets'::regclass, 'insert'),
  'anon cannot write assets');

-- Default privileges: a table added by a future migration is covered
-- automatically for service_role and stays closed to anon.
create table public.privilege_probe (id int primary key);
select ok(has_table_privilege('service_role', 'public.privilege_probe'::regclass, 'select'),
  'default privileges grant service_role access to newly created tables');
select ok(not has_table_privilege('anon', 'public.privilege_probe'::regclass, 'select'),
  'a newly created table is not readable by anon');

-- Row level security is deliberately off: the Go server is the only client and
-- connects as the table owner (CLAUDE.md §4). Assert it, so switching it on is
-- a conscious decision rather than a surprise that silently hides rows.
select is(
  (select count(*) from pg_class c
     join pg_namespace n on n.oid = c.relnamespace
    where n.nspname = 'public' and c.relkind = 'r' and c.relrowsecurity),
  0::bigint,
  'no public table has RLS enabled (by design -- see CLAUDE.md §4)');

select * from finish();
rollback;
