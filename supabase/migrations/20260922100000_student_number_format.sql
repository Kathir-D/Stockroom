-- The student-number format becomes a setting (TEMPLATE-TODO Phase B).
--
-- Until now NormalizeStudentNumber accepted digits only, 1 to 32 of them. That
-- is correct for the school this was built for and it means Stockroom **does
-- not work at all** for a school whose IDs look like `AB12345`: the sign-in
-- field filters their ID down to `12345` or to nothing, the scan-vs-typed
-- comparison then disagrees with the field, and there is no error message
-- anywhere that says what happened. Scan-to-sign-in is the headline feature,
-- so this is a hard blocker rather than a preference.
--
-- Two columns rather than one, because the third option needs somewhere to put
-- its pattern:
--
--   digits        the default, and exactly today's behaviour
--   alphanumeric  letters and digits, which covers most of the rest
--   custom        a regular expression the admin supplies and tests
--
-- The default is `digits`, so nothing changes for the existing install. That
-- direction matters: a migration that widened the format for everybody would
-- silently start accepting mis-scans as valid numbers on a machine whose cards
-- are all six digits.

alter table app_settings
  add column if not exists student_number_format  text not null default 'digits',
  add column if not exists student_number_pattern text;

-- The check lives here as well as in Go for the same reason the backup bounds
-- do: a hand-written UPDATE in Studio goes through the database and not
-- through the endpoint.
do $$
begin
  if not exists (
    select 1 from pg_constraint where conname = 'app_settings_student_number_format_check'
  ) then
    alter table app_settings
      add constraint app_settings_student_number_format_check
      check (student_number_format in ('digits', 'alphanumeric', 'custom'));
  end if;
end $$;

comment on column app_settings.student_number_format is
  'digits | alphanumeric | custom. Decides what NormalizeStudentNumber accepts and what the sign-in field filters to.';
comment on column app_settings.student_number_pattern is
  'A Go regular expression, used only when student_number_format = custom. Anchored at both ends by the server, so a pattern matching a substring cannot let a longer value through.';
