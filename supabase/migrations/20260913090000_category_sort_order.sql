-- Browse-list ordering (CLAUDE.md §13, 2026-09-12): the category filters and
-- the asset list follow Catagories.md's document order -- Cameras/Bodies,
-- Lenses, Lights, Audio Stuff, Physical Bags, Tripods/Monopods, Batteries,
-- Misc -- not alphabetical order. Nothing in the table recorded that order, so
-- it gets a column of its own.
--
-- sort_order is a position among siblings: it is compared only against rows
-- with the same parent_id, so gaps and duplicates are harmless and the numbers
-- need not restart at 1 under each parent. A tie falls back to the name, so a
-- level that was never numbered still comes out alphabetically instead of
-- arbitrarily.

alter table categories add column if not exists sort_order integer not null default 0;

comment on column categories.sort_order is
  'Position among siblings (same parent_id), ascending; ties break on name. Catagories.md''s document order at the Type level.';

-- Backfill in insertion order, which for the seeded tree is document order.
-- created_at alone is not enough to separate rows inserted by one statement --
-- now() is the transaction clock, so a whole level shares a timestamp -- hence
-- ctid as the tiebreak. Best-effort by nature: the seed sets real numbers, and
-- Phase 5's category CRUD lets an admin renumber a level by hand.
update categories c
set sort_order = o.position
from (
  select id, (row_number() over (partition by parent_id order by created_at, ctid))::int as position
  from categories
) o
where o.id = c.id;
