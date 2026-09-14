-- asset_tag becomes an internal, auto-generated key; serial_number becomes the
-- one identifier anybody types, reads or scans.
--
-- Why: the two columns were doing the same job. `asset_tag` came from the
-- original schema, written before the barcode flow was settled, when it was
-- going to be *the* per-unit identifier. `serial_number` then took that role --
-- it is what the sticker encodes and the only thing `ScanItem` looks up
-- (CLAUDE.md §1.5) -- and `asset_tag` stayed behind as a second unique code the
-- admin form still demanded. Entering real inventory would have meant typing
-- two identifiers per unit, one of which nobody ever reads.
--
-- The default lives here rather than in Go on purpose. Every writer -- the
-- admin panel, seed.sql, a future CSV import, a hand-written INSERT in Studio --
-- gets a tag without knowing it has to, which is what "backend-owned" has to
-- mean for it to stay true.

create sequence if not exists assets_asset_tag_seq;

-- Start the sequence past anything already in the table, so a generated tag can
-- never collide with a hand-written one from before this migration.
select setval(
  'assets_asset_tag_seq',
  greatest(
    (select count(*) from assets),
    (select coalesce(max(substring(asset_tag from '[0-9]+$')::bigint), 0) from assets
      where asset_tag ~ '[0-9]+$')
  ) + 1,
  false
);

alter table assets
  alter column asset_tag set default 'AST-' || lpad(nextval('assets_asset_tag_seq')::text, 6, '0');

-- serial_number is now required, because it is what the UI shows and what the
-- scanner reads. Backfill first: any row without one falls back to the tag it
-- already had, which is unique, so the constraint can go on without dropping
-- data. (In practice this touches nothing -- the seed has always set serials.)
update assets set serial_number = asset_tag where serial_number is null;

alter table assets alter column serial_number set not null;

comment on column assets.asset_tag is
  'Internal auto-generated key (AST-000001). Never shown to students and never '
  'typed by an admin; serial_number is the user-facing identifier and the scan key.';

comment on column assets.serial_number is
  'The scan key. Barcode stickers encode this, and ScanItem looks up nothing else.';
