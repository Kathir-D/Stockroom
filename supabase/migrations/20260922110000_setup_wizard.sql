-- The first-run wizard's progress (TEMPLATE-TODO Phase B).
--
-- Two columns on the settings row rather than a table: there is one wizard and
-- one installation. `setup_step` is where to resume, because the wizard saves
-- as each step completes and closing the laptop half way is expected.
-- `setup_completed_at` null means "an admin signing in is taken to the wizard".
--
-- An installation that already has accounts when this runs is one somebody set
-- up by hand long before the wizard existed, so it is marked complete here --
-- otherwise the next admin to sign in at an existing school would be walked
-- through a first run of a system with three hundred items in it. A fresh
-- install has no accounts at this point, because the server applies
-- migrations before it creates the failsafe admin. In development,
-- `supabase db reset` runs this before seed.sql, which marks it complete
-- itself.
alter table app_settings
  add column if not exists setup_step integer not null default 0
    check (setup_step between 0 and 20),
  add column if not exists setup_completed_at timestamptz;

update app_settings
   set setup_completed_at = now()
 where setup_completed_at is null
   and exists (select 1 from profiles);

comment on column app_settings.setup_step is
  'The first-run wizard step to resume at. Only meaningful while setup_completed_at is null.';
comment on column app_settings.setup_completed_at is
  'When the first-run wizard was finished or dismissed. Null sends an admin to it after sign-in.';
