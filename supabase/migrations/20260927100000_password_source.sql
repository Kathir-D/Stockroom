-- Who chose each password, and when (ROADMAP §3.1).
--
-- A roster-imported student has no password until their first scan sign-in
-- sets one, so whoever scans the number first chooses it. Admin -> Users
-- shows this column so an admin can see a password a student set themselves
-- and ask them whether they did. owner = set at first scan sign-in (or by the
-- first admin in the setup wizard), admin = an admin reset, failsafe = the
-- .env failsafe admin. Null for passwords set before this was recorded.
alter table profiles
  add column password_set_at timestamptz,
  add column password_set_by text check (password_set_by in ('owner', 'admin', 'failsafe'));
