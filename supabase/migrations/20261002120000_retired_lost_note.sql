-- Why an item was retired (CLAUDE.md §7).
--
-- Retiring recorded only when. An admin retiring an item now says whether it
-- is lost and may leave a note, and both stay on the item so the Assets table
-- can show them beside a retired row. Bringing the item back clears them.
alter table assets
  add column retired_lost boolean not null default false,
  add column retired_note text;
