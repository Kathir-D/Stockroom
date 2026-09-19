-- Kits (TODO Phase 8, CLAUDE.md §2 "lowest priority, kept in scope if time
-- allows"): a named bundle of assets -- "Kit #1 = this camera + this lens +
-- this bag" -- added to the cart and returned as one unit.
--
-- The tables have been in the schema since the base migration and nothing was
-- ever written against them. Both rules below are new; neither can be expressed
-- by the original DDL, and both are the kind that must not be able to drift, so
-- they live in the database rather than only in Go.

-- A kit's name is what somebody reads off a label on a bag, so two kits sharing
-- one is a mistake rather than a choice. Case-insensitive, because "Kit #1" and
-- "kit #1" name the same bag and only one of them is on the tape.
create unique index if not exists kits_name_lower_key on kits (lower(name));

comment on index kits_name_lower_key is
  'A kit name is a physical label; two kits cannot share one, case ignored.';

-- An asset belongs to at most one kit.
--
-- The schema permits many, and physically it is even true that one tripod could
-- be listed in two bundles -- but a shared unit means checking out kit A
-- silently makes kit B incomplete, and that failure surfaces at the shelf, at
-- checkout time, to a student who cannot fix it. Refusing here moves it to the
-- moment an admin builds the kit, where the answer is obvious: buy a second
-- one, or leave it out of this kit. internal/stockroom checks first so the
-- error can name the kit the unit is already in; this index is the floor under
-- that check.
create unique index if not exists kit_items_asset_key on kit_items (asset_id);

comment on index kit_items_asset_key is
  'One kit per asset: a unit shared between kits makes the second kit incomplete without saying so.';
