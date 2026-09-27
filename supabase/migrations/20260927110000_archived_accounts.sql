-- Archived accounts (ROADMAP §3.4).
--
-- custody_events names profiles in three columns with no delete rule, so an
-- account that ever borrowed anything cannot be deleted, and the roster import
-- only adds and updates. Every graduate kept a working scan sign-in for good.
-- An archived account cannot sign in and is left out of the user pickers, and
-- its custody history stays exactly as it was. A roster import that names the
-- number again brings it back.
alter table profiles add column archived_at timestamptz;

create index idx_profiles_active on profiles (student_number) where archived_at is null;
