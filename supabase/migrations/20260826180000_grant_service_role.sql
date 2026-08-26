-- Stockroom uses a single trusted local workstation with no public network exposure.
-- The app authenticates with the service_role key directly (bypasses RLS) and
-- enforces role permissions (owner/executive_producer/producer/member) in app code
-- instead of via RLS policies. Revoke anon/authenticated access since they're unused.

grant usage on schema public to service_role;
grant all privileges on all tables in schema public to service_role;
grant all privileges on all sequences in schema public to service_role;
grant execute on all functions in schema public to service_role;

alter default privileges in schema public grant all privileges on tables to service_role;
alter default privileges in schema public grant all privileges on sequences to service_role;
alter default privileges in schema public grant execute on functions to service_role;

revoke all on all tables in schema public from anon, authenticated;
