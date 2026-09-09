-- v1 flow (CLAUDE.md §6.2). Additive only: nothing is dropped or renamed.
--
-- profiles gains the student number that the ID-card barcode encodes (the
-- login key for both scan and typed sign-in), split name fields, a photo path
-- under uploads/, and the single permission flag is_admin. The 4-tier role
-- column stays but nothing reads it. email becomes optional because the
-- roster CSV does not carry one.
--
-- assets gains a photo path and a unique serial number: the serial is what
-- the barcode sticker encodes, so two units can never share one. Postgres
-- unique indexes allow many nulls, so assets without a sticker yet still
-- insert.
--
-- asset_status gains 'unavailable', the v1 catch-all for broken, missing and
-- retired units. The older labels stay in the enum, unused.

alter table profiles
  add column student_number text unique,
  add column first_name text,
  add column last_name text,
  add column photo_path text,
  add column is_admin boolean not null default false;

alter table profiles alter column email drop not null;

alter table assets add column photo_path text;

create unique index idx_assets_serial on assets (serial_number);

alter type asset_status add value 'unavailable';
