-- Historical (CLAUDE.md §4). Written when the Svelte frontend talked to
-- PostgREST with the service_role key; nothing has used that path since Phase
-- 6, and only the Go server, connecting as `postgres`, reaches the database
-- now. The file stays because removing an applied migration does not unapply
-- it, and because `supabase db reset` replays this directory from empty.
--
-- Guarded on the roles existing, because they do not on a plain Postgres.
-- `service_role`, `anon` and `authenticated` are created by the Supabase
-- stack, and an installed Stockroom runs one `postgres:17` container with none
-- of it (TEMPLATE-TODO Phase A). Unguarded, this file is the one thing standing
-- between a fresh install and a working schema -- it would fail on role
-- "service_role" does not exist, the server would stop at migration two of
-- ten, and the error would name a Supabase concept the person reading it has
-- deliberately never installed.
--
-- `execute` rather than plain statements: GRANT and ALTER DEFAULT PRIVILEGES
-- name the role as an identifier, which is parsed when the block is compiled,
-- so an `if exists` around a literal GRANT would still fail at parse time on a
-- database where the role is absent.
do $$
begin
  if exists (select 1 from pg_roles where rolname = 'service_role') then
    execute 'grant usage on schema public to service_role';
    execute 'grant all privileges on all tables in schema public to service_role';
    execute 'grant all privileges on all sequences in schema public to service_role';
    execute 'grant execute on all functions in schema public to service_role';

    execute 'alter default privileges in schema public grant all privileges on tables to service_role';
    execute 'alter default privileges in schema public grant all privileges on sequences to service_role';
    execute 'alter default privileges in schema public grant execute on functions to service_role';
  end if;

  if exists (select 1 from pg_roles where rolname = 'anon') then
    execute 'revoke all on all tables in schema public from anon';
  end if;
  if exists (select 1 from pg_roles where rolname = 'authenticated') then
    execute 'revoke all on all tables in schema public from authenticated';
  end if;
end $$;
