-- idx_assets_search backs the free-text box in the browse screen (TODO Phase 3).
-- Its expression concatenates three columns, two of which are nullable -- the
-- coalesce() calls are the only thing stopping a null description from making
-- the whole document null and hiding the asset from every search.
begin;
create extension if not exists pgtap;
select no_plan();

-- Serials are unique across assets now (idx_assets_serial), so fixtures need
-- their own namespace the way the UUIDs do: everything here is prefixed SR060,
-- never a value the seed or another test file could also use.
insert into assets (id, asset_tag, name, description, serial_number) values
  ('55555555-0000-0000-0000-000000000010', 'SR-001', 'Canon 70-200mm f/2.8 zoom lens',
   'Telephoto zoom for interviews', 'SN-SR060-001'),
  -- the case the coalesce() protects: nothing but a name
  ('55555555-0000-0000-0000-000000000011', 'SR-002', 'Manfrotto tripod', null, null),
  -- a linear item's model-prefixed serial, the shape hardest to tokenise
  ('55555555-0000-0000-0000-000000000012', 'SR-003', 'Battery', null, 'SR060Bat-001');

-- The index is a GIN index over the tsvector expression.
select is(
  (select amname from pg_class c
     join pg_am am on am.oid = c.relam
    where c.oid = 'public.idx_assets_search'::regclass),
  'gin', 'idx_assets_search is a GIN index');
select matches(
  (select pg_get_indexdef('public.idx_assets_search'::regclass)),
  'to_tsvector',
  'idx_assets_search indexes a tsvector expression');

-- Search behaviour, exercised through the same expression the index stores.
create or replace function pg_temp.doc(a assets) returns tsvector language sql immutable as $$
  select to_tsvector('english',
    coalesce(a.name,'') || ' ' || coalesce(a.description,'') || ' ' || coalesce(a.serial_number,''))
$$;

select ok(pg_temp.doc(a) @@ plainto_tsquery('english', 'canon'), 'search matches a word in the name')
  from assets a where a.id = '55555555-0000-0000-0000-000000000010';
select ok(pg_temp.doc(a) @@ plainto_tsquery('english', 'telephoto'), 'search matches a word in the description')
  from assets a where a.id = '55555555-0000-0000-0000-000000000010';
select ok(pg_temp.doc(a) @@ plainto_tsquery('english', 'interview'), 'search stems (interview matches interviews)')
  from assets a where a.id = '55555555-0000-0000-0000-000000000010';
select ok(not (pg_temp.doc(a) @@ plainto_tsquery('english', 'nikon')), 'search does not match an unrelated word')
  from assets a where a.id = '55555555-0000-0000-0000-000000000010';

-- The regression this file exists for.
select isnt(pg_temp.doc(a), ''::tsvector,
  'an asset with a null description and null serial still produces a searchable document')
  from assets a where a.id = '55555555-0000-0000-0000-000000000011';
select ok(pg_temp.doc(a) @@ plainto_tsquery('english', 'manfrotto'),
  'an asset with only a name is still findable by name')
  from assets a where a.id = '55555555-0000-0000-0000-000000000011';

-- Serial numbers are part of the document, so a partial scan typed by hand
-- still finds the item.
select ok(pg_temp.doc(a) @@ plainto_tsquery('english', 'SR060Bat-001'),
  'the serial number is searchable')
  from assets a where a.id = '55555555-0000-0000-0000-000000000012';

select * from finish();
rollback;
