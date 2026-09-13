-- Sample data for local development, loaded by `supabase db reset`. Not for
-- production: the real inventory is entered through the admin panel and the
-- real roster through the CSV import.
--
-- Category tree: Type -> Category -> Model, from Catagories.md. Every physical
-- unit is an asset whose category_id points at a Model node. Names are unique
-- across the whole table, so parents are looked up by name below.
--
-- Every Type and Model name here is Catagories.md's, unchanged. The Category
-- level is only partly its: Catagories.md names the three under Lenses (Zooms,
-- Primes, Accessories) and the one under Cameras/Bodies (Camera Model), but
-- lists models straight under the other six types. Those Categories are
-- invented here so every branch is the same depth and the browse filter has a
-- middle level to show:
--
--   Lights              -> Studio Lights, Light Modifiers
--   Audio Stuff         -> Wireless Mics, Wired Mics
--   Physical Bags, etc. -> Bags
--   Tripods/Monopods    -> Tripods, Gimbals
--   Batteries           -> Camera Batteries
--   Misc                -> Other
--
-- Splitting a type in two (Lights, Audio Stuff, Tripods/Monopods) is a reading
-- of the inventory, not a rule; rename or merge them freely, they hold no data
-- of their own. Primes is seeded with no Models under it because Catagories.md
-- says there are none in inventory yet, so not every branch reaches depth 3.
--
-- Accounts: one admin and one student. Both sign in by scanning their student
-- number. The admin also has the typed-login password "stockroom"; the
-- student has no password yet and is prompted to set one on first scan login,
-- which is the roster-import case.

-- Types --------------------------------------------------------------------
-- sort_order is a row's position among its siblings (categories.sort_order):
-- these eight are Catagories.md's document order, which is what the browse
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
insert into profiles (id, student_number, first_name, last_name, full_name, email, is_admin, password_hash) values
  -- typed-login password: stockroom
  ('00000000-0000-0000-0000-000000000020', '100001', 'Sam', 'Admin', 'Sam Admin', 'admin@school.edu', true,
   '$2a$10$RIHOh/Py/Duk4G21NWUEmeaQ3rYgkdFCG8QhtzxHcxFWVi22jxXdW'),
  -- no password yet: first scan login asks for one
  ('00000000-0000-0000-0000-000000000021', '200001', 'Jordan', 'Student', 'Jordan Student', null, false, null);

-- Assets -------------------------------------------------------------------
-- Serial numbers are what the barcode stickers encode. Linear items (batteries,
-- bags) use model-prefixed serials.
insert into assets (id, asset_tag, name, description, category_id, status, serial_number, created_by) values
  ('00000000-0000-0000-0000-000000000101', 'CAM-001', 'Canon T7i #1', 'DSLR body, kit lens not included',
   (select id from categories where name = 'T7i'), 'available', 'T7i-001', '00000000-0000-0000-0000-000000000020'),
  ('00000000-0000-0000-0000-000000000102', 'CAM-002', 'Canon T7i #2', 'DSLR body, kit lens not included',
   (select id from categories where name = 'T7i'), 'checked_out', 'T7i-002', '00000000-0000-0000-0000-000000000020'),
  ('00000000-0000-0000-0000-000000000103', 'CAM-003', 'Canon 5D Mark IV', 'Full-frame DSLR body',
   (select id from categories where name = '5D Mark IV'), 'available', '5D4-001', '00000000-0000-0000-0000-000000000020'),
  ('00000000-0000-0000-0000-000000000104', 'CAM-004', 'GoPro', 'Action camera with mount',
   (select id from categories where name = 'GoPro'), 'unavailable', 'GOPRO-001', '00000000-0000-0000-0000-000000000020'),
  ('00000000-0000-0000-0000-000000000105', 'LEN-001', 'Canon 70-200mm f/2.8', 'Telephoto zoom',
   (select id from categories where name = 'Canon 70-200mm f/2.8'), 'available', 'CN70200-001', '00000000-0000-0000-0000-000000000020'),
  ('00000000-0000-0000-0000-000000000106', 'LEN-002', 'Tamron 18-400mm', 'All-in-one zoom',
   (select id from categories where name = 'Tamron 18-400mm'), 'available', 'TM18400-001', '00000000-0000-0000-0000-000000000020'),
  ('00000000-0000-0000-0000-000000000107', 'AUD-001', 'DJI Wireless Lavalier', 'Two transmitters, one receiver',
   (select id from categories where name = 'DJI Wireless Lavalier'), 'available', 'DJIMIC-001', '00000000-0000-0000-0000-000000000020'),
  ('00000000-0000-0000-0000-000000000108', 'LIT-001', 'Ring Light w/stand', 'Neewer ring light with stand',
   (select id from categories where name = 'Ring Light w/stand - Neewer'), 'available', 'RING-001', '00000000-0000-0000-0000-000000000020'),
  ('00000000-0000-0000-0000-000000000109', 'TRI-001', 'Large tripod #1', 'Video tripod with fluid head',
   (select id from categories where name = 'Large tripods'), 'available', 'TRI-001', '00000000-0000-0000-0000-000000000020'),
  ('00000000-0000-0000-0000-000000000110', 'BAT-001', 'Canon battery (T7i) #1', 'LP-E17',
   (select id from categories where name = 'Canon Camera Batteries'), 'available', 'T7iBat-001', '00000000-0000-0000-0000-000000000020'),
  ('00000000-0000-0000-0000-000000000111', 'BAT-002', 'Canon battery (T7i) #2', 'LP-E17',
   (select id from categories where name = 'Canon Camera Batteries'), 'checked_out', 'T7iBat-002', '00000000-0000-0000-0000-000000000020'),
  ('00000000-0000-0000-0000-000000000112', 'BAG-001', 'Backpack #1', 'Padded camera backpack',
   (select id from categories where name = 'Backpacks'), 'available', 'BAG-001', '00000000-0000-0000-0000-000000000020');

-- Custody ------------------------------------------------------------------
-- The two checked_out assets above are out with the student, due in three
-- days, so the browse screen and the active-custody list have something to
-- show without anyone being overdue.
insert into custody_events (asset_id, custodian_id, checked_out_by, due_at) values
  ('00000000-0000-0000-0000-000000000102', '00000000-0000-0000-0000-000000000021', '00000000-0000-0000-0000-000000000020', now() + interval '3 days'),
  ('00000000-0000-0000-0000-000000000111', '00000000-0000-0000-0000-000000000021', '00000000-0000-0000-0000-000000000020', now() + interval '3 days');
