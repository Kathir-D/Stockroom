-- Which Google account the one Drive connection is signed in to, so the admin
-- panel can say "Connected as teacher@example.com" rather than only
-- "Connected". Written when a sign-in finishes (google_admin.go), from Drive's
-- own answer about the token's owner; null for a connection made before this
-- existed, or when that lookup failed.
--
-- Not a secret: it is an email address the admin typed into Google a moment
-- earlier, and it grants nothing. It stays in the backup export.
alter table app_settings
  add column if not exists google_account text;
