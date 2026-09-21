-- A second one-time marker, for the sign-in photo wall's folder only
-- (docs/design/signin-photo-wall.html §8).
--
-- env_seeded cannot speak for this column, and the reason is a date. That
-- marker and the three backup values it guards shipped together
-- (20260917000000), so its own migration could say "there is no installation
-- that has been seeded but is not marked" and skip the backfill. The photo
-- wall's folder arrived a day later (20260918100000), by which time every
-- database that had started the server once already had env_seeded = true --
-- so adding signin_photos_folder_id to that same UPDATE gave it a WHERE
-- clause that is false on precisely the installations it was written for.
-- SIGNIN_PHOTOS_FOLDER_ID would pre-fill a database created after this
-- feature and be silently ignored on every database created before it, which
-- is not the first-boot seed §8 and CLAUDE.md §9 both describe.
--
-- A column per seeding pass rather than a version number on one: each pass is
-- one-shot on its own terms, a future value added later gets its own marker
-- for the same reason this one exists, and "has this pass run" stays a
-- question the WHERE clause can answer without knowing the order they shipped
-- in.
--
-- Default false, no backfill, exactly like env_seeded: an existing database
-- has never run this pass, so false is the truth about it. What it does NOT
-- do is re-open the backup columns -- those keep their own marker, and an
-- admin who cleared drive_remote yesterday does not get it back because the
-- photo wall gained a seed today.
alter table app_settings
  add column if not exists signin_photos_env_seeded boolean not null default false;

comment on column app_settings.signin_photos_env_seeded is
  'True once EnsureSettings has run its one SIGNIN_PHOTOS_FOLDER_ID seeding pass. Separate from env_seeded because the folder column shipped after that marker was already true on existing installs.';
