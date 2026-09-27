-- Retired items (CLAUDE.md §7).
--
-- custody_events.asset_id cascades, so deleting an item deleted its custody
-- history, which works against "full custody history on every asset". Retiring
-- is the normal way out of the catalogue now: the item is unavailable, hidden
-- from browse, and keeps every custody row. Delete stays for an item that was
-- never checked out, such as one added by mistake.
alter table assets add column retired_at timestamptz;
