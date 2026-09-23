-- DEMO DATA, FOR DEVELOPMENT ONLY. Loaded by `supabase db reset` and by
-- nothing else. An install (scripts/install.sh, docs/INSTALL.md) never runs
-- this file: a school's database starts empty, and the only way in is the
-- first-run wizard or the .env failsafe admin (CLAUDE.md §7). That matters
-- because this file ships two accounts with the password "password", and a
-- known login on a closet PC is the one thing the install must never have.
--
-- Category tree: Type -> Category -> Model, identical to
-- examples/categories.media-department.md. The two are held to each other by
-- examples_test.go, which imports that file over this seed and expects it to
-- create nothing. Every physical unit is an asset whose category_id points at
-- a Model node. Names are unique across the whole table, so parents are looked
-- up by name below.
--
-- The media department's own list named the Categories under Lenses (Zooms,
-- Primes, Accessories) and Cameras/Bodies (Camera Model) but listed models
-- straight under the other six types. These middle levels were invented so
-- every branch is the same depth and the browse filter has a level to show:
--
--   Lights              -> Studio Lights, Light Modifiers
--   Audio Stuff         -> Wireless Mics, Wired Mics
--   Physical Bags, etc. -> Bags
--   Tripods/Monopods    -> Tripods, Gimbals
--   Batteries           -> Camera Batteries
--   Misc                -> Other
--
-- Splitting a type in two is a reading of the inventory, not a rule; rename or
-- merge them freely, they hold no data of their own. Primes is seeded with no
-- Models under it because the department has none yet, so not every branch
-- reaches depth 3.
--
-- Accounts: one admin (123456) and one student (234567). Both sign in by
-- scanning their student number, and both also have the typed-login password
-- "password".
--
-- Note what this no longer demonstrates: a roster-imported account starts with
-- password_hash = null and is prompted to set one at its first scan login
-- (CLAUDE.md §7). Neither seeded account is in that state any more, so to
-- exercise that flow, create a user in the admin panel -- new accounts are
-- created password-less -- and scan their number.

-- Types --------------------------------------------------------------------
-- sort_order is a row's position among its siblings (categories.sort_order):
-- these eight are examples/categories.media-department.md's document order, which is what the browse
-- filters and the asset list sort by.
insert into categories (name, sort_order) values
  ('Cameras/Bodies',      1),
  ('Lenses',              2),
  ('Lights',              3),
  ('Audio Stuff',         4),
  ('Physical Bags, etc.', 5),
  ('Tripods/Monopods',    6),
  ('Batteries',           7),
  ('Misc',                8);

-- Categories ---------------------------------------------------------------
insert into categories (name, parent_id, sort_order) values
  ('Camera Model',      (select id from categories where name = 'Cameras/Bodies'),  1),
  ('Zooms',             (select id from categories where name = 'Lenses'),  1),
  ('Primes',            (select id from categories where name = 'Lenses'),  2),
  ('Accessories',       (select id from categories where name = 'Lenses'),  3),
  ('Studio Lights',     (select id from categories where name = 'Lights'),  1),
  ('Light Modifiers',   (select id from categories where name = 'Lights'),  2),
  ('Wireless Mics',     (select id from categories where name = 'Audio Stuff'),  1),
  ('Wired Mics',        (select id from categories where name = 'Audio Stuff'),  2),
  ('Bags',              (select id from categories where name = 'Physical Bags, etc.'),  1),
  ('Tripods',           (select id from categories where name = 'Tripods/Monopods'),  1),
  ('Gimbals',           (select id from categories where name = 'Tripods/Monopods'),  2),
  ('Camera Batteries',  (select id from categories where name = 'Batteries'),  1),
  ('Other',             (select id from categories where name = 'Misc'),  1);

-- Models -------------------------------------------------------------------
insert into categories (name, parent_id, sort_order) values
  -- Cameras/Bodies > Camera Model
  ('T5',                                      (select id from categories where name = 'Camera Model'),  1),
  ('T5i',                                     (select id from categories where name = 'Camera Model'),  2),
  ('T7',                                      (select id from categories where name = 'Camera Model'),  3),
  ('T7i',                                     (select id from categories where name = 'Camera Model'),  4),
  ('T8',                                      (select id from categories where name = 'Camera Model'),  5),
  ('T8i',                                     (select id from categories where name = 'Camera Model'),  6),
  ('6D Mark II',                              (select id from categories where name = 'Camera Model'),  7),
  ('5D Mark IV',                              (select id from categories where name = 'Camera Model'),  8),
  ('R50',                                     (select id from categories where name = 'Camera Model'),  9),
  ('SL3',                                     (select id from categories where name = 'Camera Model'), 10),
  ('Blackmagic Pocket Cinema Camera 6K Pro',  (select id from categories where name = 'Camera Model'), 11),
  ('GoPro',                                   (select id from categories where name = 'Camera Model'), 12),
  ('DJI Drone',                               (select id from categories where name = 'Camera Model'), 13),
  -- Lenses > Zooms
  ('Tamron 18-400mm',                         (select id from categories where name = 'Zooms'),  1),
  ('Tamron 150-600mm',                        (select id from categories where name = 'Zooms'),  2),
  ('Sigma 18-35mm f/1.8',                     (select id from categories where name = 'Zooms'),  3),
  ('Sigma 150-600mm f/5-6.3',                 (select id from categories where name = 'Zooms'),  4),
  ('Canon 70-200mm f/2.8',                    (select id from categories where name = 'Zooms'),  5),
  ('Canon 55-250mm',                          (select id from categories where name = 'Zooms'),  6),
  ('Canon 75-300mm',                          (select id from categories where name = 'Zooms'),  7),
  ('Canon 18-135mm',                          (select id from categories where name = 'Zooms'),  8),
  ('Canon 17-40mm f/1.4',                     (select id from categories where name = 'Zooms'),  9),
  ('Canon 18-55mm',                           (select id from categories where name = 'Zooms'), 10),
  -- Lenses > Primes: none in inventory yet; the Category node stays so the
  -- filter shows it
  -- Lenses > Accessories
  ('Sigma Teleconverter',                     (select id from categories where name = 'Accessories'),  1),
  -- Lights > Studio Lights
  ('Softbox Lights w/stand - Interfit',       (select id from categories where name = 'Studio Lights'),  1),
  ('LED light box w/stand - Neewer',          (select id from categories where name = 'Studio Lights'),  2),
  ('Ring Light w/stand - Neewer',             (select id from categories where name = 'Studio Lights'),  3),
  ('LED Wand Light - ICE Light',              (select id from categories where name = 'Studio Lights'),  4),
  -- Lights > Light Modifiers
  ('Lighting Reflectors',                     (select id from categories where name = 'Light Modifiers'),  1),
  -- Audio Stuff > Wireless Mics
  ('DJI Wireless Lavalier',                   (select id from categories where name = 'Wireless Mics'),  1),
  -- Audio Stuff > Wired Mics
  ('Wired Rode Mics',                         (select id from categories where name = 'Wired Mics'),  1),
  ('Komika Phone Mics',                       (select id from categories where name = 'Wired Mics'),  2),
  ('Sennheiser Wired Lav Mics',               (select id from categories where name = 'Wired Mics'),  3),
  -- Physical Bags, etc. > Bags
  ('Backpacks',                               (select id from categories where name = 'Bags'),  1),
  ('Canon Small Bags',                        (select id from categories where name = 'Bags'),  2),
  -- Tripods/Monopods > Tripods
  ('Cell phone tripod',                       (select id from categories where name = 'Tripods'),  1),
  ('Large tripods',                           (select id from categories where name = 'Tripods'),  2),
  ('Monopods',                                (select id from categories where name = 'Tripods'),  3),
  -- Tripods/Monopods > Gimbals
  ('Big Gimbal',                              (select id from categories where name = 'Gimbals'),  1),
  ('DJI phone gimbal',                        (select id from categories where name = 'Gimbals'),  2),
  -- Batteries > Camera Batteries
  ('Canon Camera Batteries',                  (select id from categories where name = 'Camera Batteries'),  1),
  -- Misc > Other
  ('Dolly',                                   (select id from categories where name = 'Other'),  1);

-- Accounts -----------------------------------------------------------------
-- Fixed ids so tests and custody rows below can reference them.
-- Both have the typed-login password `password` (bcrypt, cost 10). Development
-- credentials for a localhost-only stack; the real closet PC gets its accounts
-- from the roster import and the .env failsafe admin (CLAUDE.md §7), never from
-- this file.
--
-- Either can also sign in by *scanning* their number, which needs no password
-- at all -- that is the primary path (CLAUDE.md §1.1).
insert into profiles (id, student_number, first_name, last_name, full_name, email, is_admin, password_hash) values
  ('00000000-0000-0000-0000-000000000020', '123456', 'Admin', 'Admin', 'Admin Admin', 'admin@school.edu', true,
   '$2a$10$LTQSrWvFzne8LNk6A.E/eOtbT.ea93VFjkBtb5mPSs4Uw53eTeQCS'),
  ('00000000-0000-0000-0000-000000000021', '234567', 'Student', 'Student', 'Student Student', null, false,
   '$2a$10$4C1nAO/rblZN1Xp0Bor90.LarUxj11uo1NgxSN9JPcPDuH/R7gSj6');

-- Assets -------------------------------------------------------------------
-- One row per physical unit. **The name is the model, with no #1/#2 suffix**:
-- the browse list groups units by name, so `Canon T7i #1` and `Canon T7i #2`
-- would be two groups of one rather than one group of two, and the `2 of 3
-- available` count that makes the list worth reading would never appear.
--
-- What tells two units of the same model apart is the **serial number**, which
-- is what the barcode sticker encodes and the only thing `ScanItem` looks up
-- (CLAUDE.md §1.5, §6.2). Every unit has one, including batteries and bags,
-- which have no manufacturer serial and so take a model-prefixed one.
--
-- These serials are fabricated stand-ins in a plausible shape, to be replaced
-- with the real inventory. Some are deliberately long, so the middle-truncation
-- in <Serial> (`3QZB…8842`, full value on hover) has something to show.
-- No asset_tag column: it is generated by the database (migration
-- 20260914120000), and the seed goes through the same default every other
-- writer does rather than inventing its own scheme.
insert into assets (id, name, description, category_id, status, serial_number, created_by) values
  -- Cameras/Bodies. Three T7i bodies, one of them out: the browse row reads
  -- "2 of 3 available" and expands to three units with different serials.
  ('00000000-0000-0000-0000-000000000101', 'Canon T7i', 'DSLR body, kit lens not included',
   (select id from categories where name = 'T7i'), 'available', '042021001234', '00000000-0000-0000-0000-000000000020'),
  ('00000000-0000-0000-0000-000000000102', 'Canon T7i', 'DSLR body, kit lens not included',
   (select id from categories where name = 'T7i'), 'checked_out', '042021007781', '00000000-0000-0000-0000-000000000020'),
  ('00000000-0000-0000-0000-000000000113', 'Canon T7i', 'DSLR body, kit lens not included',
   (select id from categories where name = 'T7i'), 'available', '042021009902', '00000000-0000-0000-0000-000000000020'),
  ('00000000-0000-0000-0000-000000000103', 'Canon 5D Mark IV', 'Full-frame DSLR body',
   (select id from categories where name = '5D Mark IV'), 'available', '182055400917', '00000000-0000-0000-0000-000000000020'),
  ('00000000-0000-0000-0000-000000000104', 'GoPro', 'Action camera with mount',
   (select id from categories where name = 'GoPro'), 'unavailable', 'C3341326049871', '00000000-0000-0000-0000-000000000020'),

  -- Lenses
  ('00000000-0000-0000-0000-000000000105', 'Canon 70-200mm f/2.8', 'Telephoto zoom',
   (select id from categories where name = 'Canon 70-200mm f/2.8'), 'available', '7820001466', '00000000-0000-0000-0000-000000000020'),
  ('00000000-0000-0000-0000-000000000106', 'Tamron 18-400mm', 'All-in-one zoom',
   (select id from categories where name = 'Tamron 18-400mm'), 'available', '018400B028711', '00000000-0000-0000-0000-000000000020'),

  -- Audio. A long serial, to exercise the middle-truncation.
  ('00000000-0000-0000-0000-000000000107', 'DJI Wireless Lavalier', 'Two transmitters, one receiver',
   (select id from categories where name = 'DJI Wireless Lavalier'), 'available', '3QZBK2A0021C8842', '00000000-0000-0000-0000-000000000020'),

  -- Lights
  ('00000000-0000-0000-0000-000000000108', 'Ring Light w/stand', 'Neewer ring light with stand',
   (select id from categories where name = 'Ring Light w/stand - Neewer'), 'available', 'NW18RL0041', '00000000-0000-0000-0000-000000000020'),

  -- Tripods. Two of the same model, both free.
  ('00000000-0000-0000-0000-000000000109', 'Large tripod', 'Video tripod with fluid head',
   (select id from categories where name = 'Large tripods'), 'available', 'TRIPOD-001', '00000000-0000-0000-0000-000000000020'),
  ('00000000-0000-0000-0000-000000000114', 'Large tripod', 'Video tripod with fluid head',
   (select id from categories where name = 'Large tripods'), 'available', 'TRIPOD-002', '00000000-0000-0000-0000-000000000020'),

  -- Batteries. No manufacturer serial, so the sticker carries a model-prefixed
  -- one; four units, one out, so the row reads "3 of 4 available".
  ('00000000-0000-0000-0000-000000000110', 'Canon battery (T7i)', 'LP-E17',
   (select id from categories where name = 'Canon Camera Batteries'), 'available', 'T7IBAT-001', '00000000-0000-0000-0000-000000000020'),
  ('00000000-0000-0000-0000-000000000111', 'Canon battery (T7i)', 'LP-E17',
   (select id from categories where name = 'Canon Camera Batteries'), 'checked_out', 'T7IBAT-002', '00000000-0000-0000-0000-000000000020'),
  ('00000000-0000-0000-0000-000000000115', 'Canon battery (T7i)', 'LP-E17',
   (select id from categories where name = 'Canon Camera Batteries'), 'available', 'T7IBAT-003', '00000000-0000-0000-0000-000000000020'),
  ('00000000-0000-0000-0000-000000000116', 'Canon battery (T7i)', 'LP-E17',
   (select id from categories where name = 'Canon Camera Batteries'), 'available', 'T7IBAT-004', '00000000-0000-0000-0000-000000000020'),

  -- Bags
  ('00000000-0000-0000-0000-000000000112', 'Backpack', 'Padded camera backpack',
   (select id from categories where name = 'Backpacks'), 'available', 'BAG-001', '00000000-0000-0000-0000-000000000020');

-- Custody ------------------------------------------------------------------
-- The two checked_out assets above are out with the student, due in three
-- days, so the browse screen and the active-custody list have something to
-- show without anyone being overdue.
insert into custody_events (asset_id, custodian_id, checked_out_by, due_at) values
  ('00000000-0000-0000-0000-000000000102', '00000000-0000-0000-0000-000000000021', '00000000-0000-0000-0000-000000000020', now() + interval '3 days'),
  ('00000000-0000-0000-0000-000000000111', '00000000-0000-0000-0000-000000000021', '00000000-0000-0000-0000-000000000020', now() + interval '3 days');

-- Kits ---------------------------------------------------------------------
-- One kit, so the Kits screen has something to draw on a fresh `db reset` and
-- the whole-or-nothing rule has a case to demonstrate: every unit below is
-- available, so the seeded kit reads "4 of 4 available" and can go into a cart.
-- An asset belongs to exactly one kit (migration 20260918090000), so nothing
-- here may repeat a unit from another kit added later.
insert into kits (id, name, description) values
  ('00000000-0000-0000-0000-000000000301', 'Kit #1 — Interview',
   'Body, telephoto, lav mic and the bag they live in');

insert into kit_items (kit_id, asset_id) values
  ('00000000-0000-0000-0000-000000000301', '00000000-0000-0000-0000-000000000101'), -- Canon T7i
  ('00000000-0000-0000-0000-000000000301', '00000000-0000-0000-0000-000000000105'), -- Canon 70-200mm f/2.8
  ('00000000-0000-0000-0000-000000000301', '00000000-0000-0000-0000-000000000107'), -- DJI Wireless Lavalier
  ('00000000-0000-0000-0000-000000000301', '00000000-0000-0000-0000-000000000112'); -- Backpack

-- The development database is set up already; do not send the seeded admin
-- through the first-run wizard on every reset. (It is still reachable from
-- Admin -> Settings -> "Run the setup guide again".)
update app_settings set setup_completed_at = now() where id = true;
