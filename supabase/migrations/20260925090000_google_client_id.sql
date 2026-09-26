-- The Google OAuth client rclone signs in with, for both Drive features.
--
-- Every Drive call Stockroom makes -- the backup's push and the sign-in photo
-- wall's reads -- goes through one rclone remote, and until now that remote
-- used the client id compiled into rclone. rclone is retiring that shared id
-- during 2026 (https://rclone.org/drive/#making-your-own-client-id), after
-- which a remote with a blank client_id stops working. These two columns are
-- the school's own, typed into Admin → Settings and passed to both
-- `rclone authorize` and `rclone config create`.
--
-- Nullable: blank means rclone's shared id, which still works today, and the
-- settings screen says so beside the fields.
alter table app_settings
  add column if not exists google_client_id text,
  add column if not exists google_client_secret text;

comment on column app_settings.google_client_secret is
  'SECRET. Redacted from GET /admin/settings and from the backup export (exportRedactions in backup.go). A new secret column here must be added to that list too.';
