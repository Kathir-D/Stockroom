-- The sign-in photo wall's live Drive folder moves out of .env and into the
-- settings row (docs/design/signin-photo-wall.html §7, §8).
--
-- Same reasoning as the backup columns beside it: after the one-time install
-- nobody edits a file, and the folder is the one value about this feature that
-- a media teacher will actually want to change -- when the department shoots a
-- new season, the wall should follow it. .env keeps the seeding job only
-- (EnsureSettings), so SIGNIN_PHOTOS_FOLDER_ID pre-fills a fresh machine and
-- is then ignored forever.

alter table app_settings
  -- The folder id out of a Drive share link. THIS IS A CREDENTIAL, not a
  -- label: a folder shared "anyone with the link" is readable by whoever holds
  -- the link, so the id is never sent to a client, never rendered, never
  -- pre-filled into the admin panel's field, and never written to
  -- activity_log. It is in exportRedactions (internal/stockroom/backup.go)
  -- for the same reason github_token is -- an unredacted export would push the
  -- key to the folder into a repository.
  add column if not exists signin_photos_folder_id text,

  -- What the admin panel shows instead. A label a person typed ("Fall 2026
  -- game photos"), which can be read but cannot be used to open anything.
  --
  -- Deliberately not fetched from Drive. rclone addresses Drive by path from a
  -- configured root and has no "stat this file id" command, so with a bare
  -- folder id there is no way to ask Google what the folder is called without
  -- writing the Google API client §3 exists to avoid. A typed label is the
  -- honest version of the same affordance.
  add column if not exists signin_photos_label text,

  -- Who last replaced the folder, and when. The admin screen reports both, and
  -- the activity_log row written at the same moment carries the label -- never
  -- the id, since the log is itself exported in backups and not echoing the
  -- link to the screen buys nothing if it is sitting in activity_log.csv.
  add column if not exists signin_photos_changed_at timestamptz,
  add column if not exists signin_photos_changed_by uuid references profiles(id) on delete set null;

comment on column app_settings.signin_photos_folder_id is
  'Google Drive folder id for the sign-in photo wall. A credential, not a label: redacted from the CSV export by exportRedactions in internal/stockroom/backup.go, and never included in any HTTP response.';
comment on column app_settings.signin_photos_label is
  'Human-readable name for that folder, typed by the admin who set it. This is what the admin panel and activity_log show in place of the id.';
